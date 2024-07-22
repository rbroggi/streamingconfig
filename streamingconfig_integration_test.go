package streamingconfig_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	config "github.com/rbroggi/streamingconfig"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type appConfigV0 struct {
	Name     string        `json:"name" default:"bobby"`
	Duration time.Duration `json:"duration"`
	Time     time.Time     `json:"time"`
	Nested   nestedConfig  `json:"nested"`
	List     []string      `json:"list"`
}

type nestedConfig struct {
	Counter int `json:"counter"`
}

func (a *appConfigV0) Update(new config.Config) error {
	newCfg, ok := new.(*appConfigV0)
	if !ok {
		return errors.New("wrong type")
	}
	a.Name = newCfg.Name
	a.Duration = newCfg.Duration
	a.Time = newCfg.Time
	a.Nested = newCfg.Nested
	a.List = newCfg.List
	return a.validate()
}

func (a *appConfigV0) validate() error {
	if a.Duration < 0 {
		return errors.New("duration must be greater than zero")
	}
	return nil
}

type appConfigV1 struct {
	Name   string       `json:"name" default:"john"`
	Time   time.Time    `json:"time"`
	Nested nestedConfig `json:"nested"`
	Age    int          `json:"age"`
}

func (a *appConfigV1) Update(new config.Config) error {
	newCfg, ok := new.(*appConfigV1)
	if !ok {
		return errors.New("wrong type")
	}
	a.Name = newCfg.Name
	a.Time = newCfg.Time
	a.Nested = newCfg.Nested
	a.Age = newCfg.Age
	return a.validate()
}

func (a *appConfigV1) validate() error {
	if a.Age < 0 {
		return errors.New("age must be greater than zero")
	}
	return nil
}

func NewTestStore[T config.Config](
	t *testing.T,
	nowProvider func() time.Time,
	db *mongo.Database,
) *config.WatchedRepo[T] {
	t.Helper()
	configStore, err := config.NewWatchedRepo(
		config.Args{
			Logger: slog.Default(),
			DB:     db,
		}, config.WithNowFn[T](nowProvider))
	require.NoError(t, err)

	return configStore
}

const mongoLocalAddr = "localhost:27017"

func newFixture(t *testing.T) *fixture {
	t.Helper()

	ctx, cnl := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cnl)
	// use test name as db name to parallel tests.
	opts := options.Client()
	opts.ApplyURI("mongodb://" + mongoLocalAddr + "/?connect=direct")
	opts.SetMaxPoolSize(50)
	client, err := mongo.Connect(ctx, opts)
	require.NoError(t, err)
	require.NoError(t, client.Ping(ctx, nil))
	require.NoError(t, err)
	db := client.Database(dbName(t))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, db.Drop(ctx))
	})

	// waiting for the replicaset to elect master
	require.EventuallyWithT(t, func(t *assert.CollectT) {
		var result bson.M
		assert.NoError(t, client.Database("admin").
			RunCommand(ctx, bson.D{primitive.E{Key: "isMaster", Value: 1}}).Decode(&result), "checking mongoDB primary status")
		assert.Equal(t, true, result["ismaster"])
	}, 15*time.Second, 100*time.Millisecond)

	return &fixture{db: db}
}

type fixture struct {
	db *mongo.Database
}

func dbName(t *testing.T) string {
	return strings.Replace(t.Name(), "/", "-", -1)
}

func Test_ConfigCreateFindAndUpdateConfiguration(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	at := time.Unix(time.Now().UTC().Unix(), 0).UTC()

	now := &at
	nowProvider := func() time.Time {
		return *now
	}
	ctx, cnl := context.WithTimeout(context.Background(), 30*time.Second)

	configStoreOne := NewTestStore[*appConfigV0](t, nowProvider, f.db)
	t.Run("cannot perform methods before starting", func(t *testing.T) {
		cfg, err := configStoreOne.GetConfig()
		require.ErrorIs(t, err, config.ErrNotStarted)
		require.Nil(t, cfg)
		cfgV, err := configStoreOne.GetLatestVersion()
		require.ErrorIs(t, err, config.ErrNotStarted)
		require.Nil(t, cfgV)
	})

	done1, err := configStoreOne.Start(ctx)
	require.NoError(t, err)

	configStoreTwo := NewTestStore[*appConfigV0](t, nowProvider, f.db)
	done2, err := configStoreTwo.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		cnl()
		doneOrTimeout(t, done1, 5*time.Second)
		doneOrTimeout(t, done2, 5*time.Second)
	})

	t.Run("default config before initialization", func(t *testing.T) {
		got, err := configStoreOne.GetLatestVersion()
		require.NoError(t, err)
		require.Equal(t, &config.Versioned[*appConfigV0]{
			Config: &appConfigV0{
				Name: "bobby",
			},
		}, got)
	})

	t.Run("create first config and see it reflected into second store", func(t *testing.T) {
		setV1 := &appConfigV0{
			Duration: 1 * time.Second,
			Time:     at,
			Nested:   nestedConfig{Counter: 3},
			List:     []string{"a", "b"},
		}
		v1WithDefaults := &appConfigV0{
			Name:     "bobby",
			Duration: 1 * time.Second,
			Time:     at,
			Nested:   nestedConfig{Counter: 3},
			List:     []string{"a", "b"},
		}
		cV1, err := configStoreOne.UpdateConfig(ctx, config.UpdateConfigCmd[*appConfigV0]{
			By:     "u1",
			Config: setV1,
		})
		require.NoError(t, err)
		require.Equal(t, &config.Versioned[*appConfigV0]{
			Version:   1,
			UpdatedBy: "u1",
			CreatedAt: at,
			Config:    v1WithDefaults,
		}, cV1)

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			gotTwo, err := configStoreTwo.GetLatestVersion()
			assert.NoError(t, err)
			assert.Equal(t, cV1, gotTwo)
		}, 5*time.Second, 100*time.Millisecond)

		t.Run("failed update due to validation failure", func(t *testing.T) {
			badConfig := &appConfigV0{Duration: -1 * time.Second}
			gotBad, err := configStoreTwo.UpdateConfig(ctx, config.UpdateConfigCmd[*appConfigV0]{
				By:     "u2",
				Config: badConfig,
			})
			require.ErrorContains(t, err, "duration")
			require.Nil(t, gotBad)
		})

		t.Run("new version successful", func(t *testing.T) {
			at = at.Add(2 * time.Second)
			now = &at
			setV2 := &appConfigV0{
				Name:     "n2",
				Duration: 2 * time.Second,
				Time:     at.Add(2 * time.Second),
				Nested:   nestedConfig{Counter: 4},
				List:     []string{"a", "b", "c"},
			}
			cV2, err := configStoreTwo.UpdateConfig(ctx, config.UpdateConfigCmd[*appConfigV0]{
				By:     "u2",
				Config: setV2,
			})
			require.NoError(t, err)
			require.Equal(t, &config.Versioned[*appConfigV0]{
				Version:   2,
				UpdatedBy: "u2",
				CreatedAt: at,
				Config:    setV2,
			}, cV2)

			require.EventuallyWithT(t, func(t *assert.CollectT) {
				gotOne, err := configStoreOne.GetLatestVersion()
				assert.NoError(t, err)
				assert.Equal(t, cV2, gotOne)
			}, 5*time.Second, 100*time.Millisecond)

			t.Run("find 2 versions", func(t *testing.T) {
				configsByVersion, err := configStoreOne.ListVersionedConfigs(ctx, config.ListVersionedConfigsQuery{
					FromVersion: 1,
					ToVersion:   3,
				})
				require.NoError(t, err)
				require.Len(t, configsByVersion, 2)
				require.Equal(t, configsByVersion[0], cV1)
				require.Equal(t, configsByVersion[1], cV2)
			})

			t.Run("find 1 version", func(t *testing.T) {
				configsByVersion, err := configStoreOne.ListVersionedConfigs(ctx, config.ListVersionedConfigsQuery{
					FromVersion: 1,
					ToVersion:   2,
				})
				require.NoError(t, err)
				require.Len(t, configsByVersion, 1)
				require.Equal(t, configsByVersion[0], cV1)
			})

			t.Run("find 1 version bis", func(t *testing.T) {
				configsByVersion, err := configStoreOne.ListVersionedConfigs(ctx, config.ListVersionedConfigsQuery{
					FromVersion: 2,
					ToVersion:   3,
				})
				require.NoError(t, err)
				require.Len(t, configsByVersion, 1)
				require.Equal(t, configsByVersion[0], cV2)
			})

			t.Run("find no version", func(t *testing.T) {
				configsByVersion, err := configStoreOne.ListVersionedConfigs(ctx, config.ListVersionedConfigsQuery{
					FromVersion: 3,
					ToVersion:   6,
				})
				require.NoError(t, err)
				require.Len(t, configsByVersion, 0)
			})
		})
	})
}

func Test_ConfigBackwardCompatibility(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	at := time.Unix(time.Now().UTC().Unix(), 0).UTC()

	now := &at
	nowProvider := func() time.Time {
		return *now
	}
	ctx, cnl := context.WithTimeout(context.Background(), 30*time.Second)

	configStoreOne := NewTestStore[*appConfigV0](t, nowProvider, f.db)
	done1, err := configStoreOne.Start(ctx)
	require.NoError(t, err)

	configStoreTwo := NewTestStore[*appConfigV1](t, nowProvider, f.db)
	done2, err := configStoreTwo.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		cnl()
		doneOrTimeout(t, done1, 5*time.Second)
		doneOrTimeout(t, done2, 5*time.Second)
	})

	t.Run("successful v0 insert reflected in v1", func(t *testing.T) {
		setV0 := &appConfigV0{
			Duration: 1 * time.Second,
			Time:     at,
			Nested:   nestedConfig{Counter: 3},
			List:     []string{"a", "b"},
		}
		v0WithDefaults := &appConfigV0{
			Name:     "bobby",
			Duration: 1 * time.Second,
			Time:     at,
			Nested:   nestedConfig{Counter: 3},
			List:     []string{"a", "b"},
		}
		cV1, err := configStoreOne.UpdateConfig(ctx, config.UpdateConfigCmd[*appConfigV0]{
			By:     "u1",
			Config: setV0,
		})
		require.NoError(t, err)
		require.Equal(t, &config.Versioned[*appConfigV0]{
			Version:   1,
			UpdatedBy: "u1",
			CreatedAt: at,
			Config:    v0WithDefaults,
		}, cV1)

		cV2 := &config.Versioned[*appConfigV1]{
			Version:   1,
			UpdatedBy: "u1",
			CreatedAt: at,
			Config: &appConfigV1{
				Name:   "john", // new default
				Time:   at,
				Nested: nestedConfig{Counter: 3},
				Age:    0,
			},
		}

		require.EventuallyWithT(t, func(t *assert.CollectT) {
			gotOne, err := configStoreTwo.GetLatestVersion()
			assert.NoError(t, err)
			assert.Equal(t, cV2, gotOne)
		}, 5*time.Second, 100*time.Millisecond)

	})
}

func doneOrTimeout(t *testing.T, done <-chan struct{}, duration time.Duration) {
	select {
	case <-done:
		return
	case <-time.After(duration):
		t.Fatal("timeout")
	}
}
