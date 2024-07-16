// Package streamingconfig provides functionality for using dynamic configuration functionalities.
package streamingconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"time"

	"github.com/creasty/defaults"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

// Config is a representation of the app configuration. It is wrapped into
// a generic configuration which includes additional fields for the sake of
// auditability (version, updated_at and updated_by).
type Config interface {
	Update(new Config) error
}

// Versioned encapsulates a version of the configuration and adds some auditing
// information on top of the configuration.
type Versioned[T Config] struct {
	// Version is used as unique ID of the config collection.
	Version uint64 `json:"version" bson:"_id"`
	// UpdatedBy author of the config update
	UpdatedBy string `json:"updated_by" bson:"updated_by"`
	// CreatedAt time of the last config update.
	CreatedAt time.Time `json:"created_at" bson:"created_at"`
	// Config embeds the application-specific configuration.
	Config T `json:"config" bson:"app_config"`
}

var (
	// ErrNotStarted is returned when a user attempts to use the store before calling the `Start` method.
	ErrNotStarted            = errors.New("config store not started - call Start() before using it")
	ErrConfigurationNotFound = errors.New("configuration not found")
	// ErrConcurrentUpdate signals that multiple repositories are attempting to change the configuration concurrently.
	ErrConcurrentUpdate = errors.New("configuration concurrently being updated by someone-else")
	// ErrTypeMustBePointer by the constructor of the repo if the provided type is not a pointer type.
	ErrTypeMustBePointer = errors.New("configuration type argument must be pointer")
)

type Args struct {
	Logger *slog.Logger
	DB     *mongo.Database
}

func WithNowFn[T Config](nf func() time.Time) func(*WatchedRepo[T]) {
	return func(w *WatchedRepo[T]) {
		w.nowFunc = nf
	}
}

func WithSkipIndexOperations[T Config]() func(*WatchedRepo[T]) {
	return func(store *WatchedRepo[T]) {
		store.skipIndexOperation = true
	}
}

func WithCollectionName[T Config](collectionName string) func(repo *WatchedRepo[T]) {
	return func(svc *WatchedRepo[T]) {
		svc.collectionName = collectionName
	}
}

type WatchedRepo[T Config] struct {
	lgr             *slog.Logger
	source          *mongo.Database
	cfg             *Versioned[T]
	cfgWithDefaults *Versioned[T]
	// optionally overrideable
	nowFunc            func() time.Time
	collectionName     string
	skipIndexOperation bool
	configs            *mongo.Collection
	started            bool
}

func NewWatchedRepo[T Config](
	args Args,
	opts ...func(*WatchedRepo[T]),
) (*WatchedRepo[T], error) {
	var zeroValue T
	typeOfT := reflect.TypeOf(zeroValue)
	if typeOfT.Kind() != reflect.Ptr {
		return nil, ErrTypeMustBePointer
	}
	wc := writeconcern.Majority()
	wc.WTimeout = writeConcernTimeout
	s := &WatchedRepo[T]{
		lgr:            args.Logger.With("struct", "WatchedRepo"),
		source:         args.DB,
		collectionName: defaultConfigurationCollectionName,
		nowFunc: func() time.Time {
			return time.Now().UTC()
		},
	}
	for _, opt := range opts {
		opt(s)
	}
	connectionOpts := options.Collection().
		SetWriteConcern(wc).
		SetBSONOptions(&options.BSONOptions{
			UseJSONStructTags: true,
		})
	s.configs = args.DB.Collection(s.collectionName, connectionOpts)

	return s, nil
}

// Start is a non-blocking call that starts the store and watches for updates on
// the persisted configurations.
//
// For graceful shutdown, cancel the input context and wait for the returned
// channel to be closed.
func (s *WatchedRepo[T]) Start(ctx context.Context) (<-chan struct{}, error) {
	if !s.skipIndexOperation {
		if err := s.createIndexes(ctx); err != nil {
			return nil, err
		}
	}

	done, err := s.watchChanges(ctx)
	if err != nil {
		return nil, err
	}
	latest, err := s.getLatest(ctx)
	if err != nil && !errors.Is(err, ErrConfigurationNotFound) {
		return nil, err
	}
	if errors.Is(err, ErrConfigurationNotFound) {
		var zeroValue T
		typeOfT := reflect.TypeOf(zeroValue)
		if typeOfT.Kind() == reflect.Ptr {
			valOfT := reflect.New(typeOfT.Elem())
			zeroValue = valOfT.Interface().(T)
		}
		s.cfg = &Versioned[T]{
			Config: zeroValue,
		}
	} else {
		s.cfg = latest
	}
	if s.cfgWithDefaults, err = copyAndSetDefaults(s.cfg); err != nil {
		return nil, err
	}
	s.started = true

	return done, nil
}

// GetConfig gets the current user-defined configuration with defaults applied to it.
func (s *WatchedRepo[T]) GetConfig() (T, error) {
	v, err := s.GetLatestVersion()
	if err != nil {
		var zero T
		return zero, err
	}
	return v.Config, err
}

// GetLatestVersion returns the latest version of the user-provided configuration
// along with auditing data.
func (s *WatchedRepo[T]) GetLatestVersion() (*Versioned[T], error) {
	if !s.started {
		return nil, ErrNotStarted
	}
	return s.cfgWithDefaults, nil
}

// ListVersionedConfigsQuery provide query parameters for listing configurations
// by version.
type ListVersionedConfigsQuery struct {
	// FromVersion version from which retrieve the configs (inclusive)
	FromVersion uint32
	// ToVersion version until which retrieve the configs (exclusive)
	ToVersion uint32
}

// ListVersionedConfigs returns a list of the user-provided configuration
// versions along with auditing data.
func (s *WatchedRepo[T]) ListVersionedConfigs(
	ctx context.Context,
	query ListVersionedConfigsQuery,
) ([]*Versioned[T], error) {
	if !s.started {
		return nil, ErrNotStarted
	}
	ctxTimeout, cnl := context.WithTimeout(ctx, operationTimeout)
	defer cnl()
	opts := options.Find()
	opts.SetSort(bson.D{{Key: "_id", Value: 1}})
	cursor, err := s.configs.Find(ctxTimeout, bson.M{
		"_id": bson.M{"$gte": query.FromVersion, "$lt": query.ToVersion},
	}, opts)
	if err != nil {
		return nil, err
	}
	configs := make([]*Versioned[T], 0)
	for cursor.Next(ctxTimeout) {
		var cfg Versioned[T]
		if err := cursor.Decode(&cfg); err != nil {
			return nil, fmt.Errorf("failed to decode config: %w", err)
		}
		if err := defaults.Set(&cfg); err != nil {
			return nil, fmt.Errorf("failed to set defaults: %w", err)
		}
		configs = append(configs, &cfg)
	}
	return configs, nil
}

// ListConfigDatesQuery provide query parameters for listing configurations
// by dates.
type ListConfigDatesQuery struct {
	// From is the time from which retrieve the configs (inclusive)
	From time.Time
	// To is the time until which retrieve the configs (exclusive)
	To time.Time
}

// ListVersionedConfigsByDate returns a list of the user-provided configuration
// versions along with auditing data.
func (s *WatchedRepo[T]) ListVersionedConfigsByDate(
	ctx context.Context,
	query ListConfigDatesQuery,
) ([]*Versioned[T], error) {
	if !s.started {
		return nil, ErrNotStarted
	}
	ctxTimeout, cnl := context.WithTimeout(ctx, operationTimeout)
	defer cnl()
	opts := options.Find()
	opts.SetSort(bson.D{{Key: "created_at", Value: 1}})
	cursor, err := s.configs.Find(ctxTimeout, bson.M{
		"created_at": bson.M{"$gte": query.From, "$lt": query.To},
	}, opts)
	if err != nil {
		return nil, err
	}
	configs := make([]*Versioned[T], 0)
	for cursor.Next(ctxTimeout) {
		var cfg Versioned[T]
		if err := cursor.Decode(&cfg); err != nil {
			return nil, fmt.Errorf("failed to decode config: %w", err)
		}
		if err := defaults.Set(&cfg); err != nil {
			return nil, fmt.Errorf("failed to set defaults: %w", err)
		}
		configs = append(configs, &cfg)
	}
	return configs, nil
}

type UpdateConfigCmd[T Config] struct {
	By     string
	Config T
}

// UpdateConfig retrieves the latest configuration, modifies it by calling the
// underlying `Update` method and creates a new updated version.
func (s *WatchedRepo[T]) UpdateConfig(ctx context.Context, cmd UpdateConfigCmd[T]) (*Versioned[T], error) {
	if !s.started {
		return nil, ErrNotStarted
	}
	ctxTimeout, cnl := context.WithTimeout(ctx, operationTimeout)
	defer cnl()
	curr, err := s.getLatest(ctxTimeout)
	if err != nil {
		if errors.Is(err, ErrConfigurationNotFound) {
			appCfg := cmd.Config
			// this is done to validate the first configuration before creating it. It
			// validates against itself.
			if err := appCfg.Update(appCfg); err != nil {
				return nil, err
			}
			initCfg := &Versioned[T]{
				Version:   1,
				UpdatedBy: cmd.By,
				CreatedAt: s.nowFunc(),
				Config:    appCfg,
			}
			if err := s.createConfig(ctxTimeout, initCfg); err != nil {
				return nil, err
			}
			toRet, err := copyAndSetDefaults(initCfg)
			if err != nil {
				return nil, err
			}
			return toRet, nil
		}
		return nil, err
	}
	updatedConfig := curr.Config
	if err := updatedConfig.Update(cmd.Config); err != nil {
		return nil, err
	}
	newVersion := &Versioned[T]{
		Version:   curr.Version + 1,
		UpdatedBy: cmd.By,
		CreatedAt: s.nowFunc(),
		Config:    updatedConfig,
	}
	if err := s.createConfig(ctxTimeout, newVersion); err != nil {
		return nil, err
	}
	toRet, err := copyAndSetDefaults(newVersion)
	if err != nil {
		return nil, err
	}
	return toRet, err
}

func (s *WatchedRepo[T]) getLatest(ctx context.Context) (*Versioned[T], error) {
	ctxTimeout, cnl := context.WithTimeout(ctx, operationTimeout)
	defer cnl()
	opts := options.Find()
	opts.SetLimit(1)
	opts.SetSort(bson.D{{Key: "_id", Value: -1}})
	cursor, err := s.configs.Find(ctxTimeout, bson.M{}, opts)
	if err != nil {
		return nil, err
	}
	configs := make([]*Versioned[T], 0)
	for cursor.Next(ctxTimeout) {
		var cfg Versioned[T]
		err = cursor.Decode(&cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to decode config: %w", err)
		}
		configs = append(configs, &cfg)
	}
	if len(configs) == 0 {
		return nil, ErrConfigurationNotFound
	}
	return configs[0], nil
}

func (s *WatchedRepo[T]) createConfig(ctx context.Context, cfg *Versioned[T]) error {
	ctxTimeout, cnl := context.WithTimeout(ctx, operationTimeout)
	defer cnl()
	_, err := s.configs.InsertOne(ctxTimeout, cfg)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrConcurrentUpdate
		}
		return fmt.Errorf("create config failed: %w", err)
	}
	return nil
}

const (
	operationTimeout                   = 5 * time.Second
	writeConcernTimeout                = 5 * time.Second
	indexCreateTimeout                 = 30 * time.Second
	defaultConfigurationCollectionName = "config"
)

//type store interface {
//	// GetLatestConfig gets the latest configuration
//	GetLatestConfig(ctx context.Context) (*Config, error)
//	// GetConfig gets the latest configuration
//	GetConfig(ctx context.Context, version uint64) (*Config, error)
//	// ListConfigs lists the configurations matching the query parameters
//	ListConfigs(ctx context.Context, query ListConfigQuery) ([]*Config, error)
//	// UpdateConfig Updates the latest config version and generates a new one with
//	// the changed fields and bumps the version.
//	UpdateConfig(ctx context.Context, updateFunc UpdateConfigFunc) error
//	// WatchConfig receives change-events for config changes
//	WatchConfig(ctx context.Context) (<-chan models.changeEvent[Config], error)
//}

func (s *WatchedRepo[T]) watchChanges(ctx context.Context) (<-chan struct{}, error) {
	done := make(chan struct{})
	cs, err := s.configs.Watch(
		ctx,
		mongo.Pipeline{},
	)
	if err != nil {
		return nil, fmt.Errorf("error watching configs: %w", err)
	}
	go func() {
		defer close(done)
		s.iterateChangeStream(ctx, cs)
	}()

	return done, nil
}

type changeStreamDto[T Config] struct {
	DocumentKey   documentKeyDto `bson:"documentKey"`
	OperationType string         `bson:"operationType"`
	FullDocument  *Versioned[T]  `bson:"fullDocument"`
}

type documentKeyDto struct {
	ID uint64 `bson:"_id,omitempty"`
}

func (s *WatchedRepo[T]) iterateChangeStream(ctx context.Context, cs *mongo.ChangeStream) {
	defer cs.Close(ctx)
	for cs.Next(ctx) {
		var dto changeStreamDto[T]
		if err := cs.Decode(&dto); err != nil {
			s.lgr.With("error", err).
				ErrorContext(ctx, "error decoding change stream element")
		} else {
			switch dto.OperationType {
			case "insert":
				s.cfg = dto.FullDocument
				if s.cfgWithDefaults, err = copyAndSetDefaults(s.cfg); err != nil {
					s.lgr.With("error", err).ErrorContext(ctx, "could not set defaults")
				}
			default:
				s.lgr.With("operationType", dto.OperationType).ErrorContext(ctx, "invalid or unexpected operation")
				continue
			}
		}
	}
}

func (s *WatchedRepo[T]) createIndexes(ctx context.Context) error {
	ctx, cnl := context.WithTimeout(context.Background(), indexCreateTimeout)
	defer cnl()

	_, err := s.configs.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "created_at", Value: 1},
		},
		Options: options.Index().SetName("idx_created_at_inc"),
	})
	if err != nil {
		return err
	}

	return nil
}

func deepCopy[T any](orig T) (T, error) {
	b, err := json.Marshal(orig)
	if err != nil {
		var zeroV T
		return zeroV, err
	}
	var copyValue T
	typeOfT := reflect.TypeOf(copyValue)
	if typeOfT.Kind() == reflect.Ptr {
		valOfT := reflect.New(typeOfT.Elem())
		copyValue = valOfT.Interface().(T)
		if err := json.Unmarshal(b, copyValue); err != nil {
			var zeroV T
			return zeroV, err
		}
		return copyValue, nil
	}
	if err := json.Unmarshal(b, &copyValue); err != nil {
		var zeroV T
		return zeroV, err
	}
	return copyValue, nil
}

func copyAndSetDefaults[T any](orig T) (T, error) {
	cp, err := deepCopy(orig)
	if err != nil {
		var zero T
		return zero, err
	}
	if err := defaults.Set(cp); err != nil {
		var zero T
		return zero, err
	}
	return cp, nil
}

// endpoint to get config, endpoint to list config versions, endpoint to update config
// GET /configs/latest - latest
// GET /configs/<version> - specific version
// GET /configs?from=<version>&to=<version> (latest can be retrieved from GET /latest)
// PUT /configs/<version>
//
// {"heartbeat_interval_duration": "40s"}

// Forward and Backward-compatible changes:
// 1. Adding field - upon initialization, we should have a default value

// app config -
// upon start, queries latest and subscribe to change-events
