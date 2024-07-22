package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	config "github.com/rbroggi/streamingconfig"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type conf struct {
	Name    string   `json:"name" default:"john"`
	Age     int      `json:"age"`
	Friends []string `json:"friends" default:"[\"mark\",\"tom\",\"jack\"]"`
}

func (c *conf) Update(new config.Config) error {
	newCfg, ok := new.(*conf)
	if !ok {
		return errors.New("wrong configuration")
	}
	c.Name = newCfg.Name
	c.Age = newCfg.Age
	c.Friends = newCfg.Friends
	return nil
}

func main() {
	port := os.Getenv("HTTP_PORT")
	if port == "" {
		port = "8080"
	}
	runnableCtx, cancelRunnables := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancelRunnables()
	repo, done := initRepo(runnableCtx)
	s := &server{repo: repo, lgr: slog.Default()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /configs/latest", s.latestConfigHandler)
	mux.HandleFunc("PUT /configs/latest", s.putConfigHandler)
	mux.HandleFunc("GET /configs", s.listConfigsHandler)
	// Create a new server
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%s", port),
		Handler: mux,
	}
	// Start the server in a goroutine
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	fmt.Printf("Server listening on port %s\n", port)
	// until shutdown signal is sent
	<-runnableCtx.Done()
	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer func() {
		cancel()
	}()

	if err := srv.Shutdown(ctx); err != nil {
		log.Println("Error during shutdown:", err)
	}
	log.Println("Server stopped")
	<-done
}

func initRepo(ctx context.Context) (*config.WatchedRepo[*conf], <-chan struct{}) {
	lgr := slog.Default()
	db := getDb()
	repo, err := config.NewWatchedRepo[*conf](
		config.Args{
			Logger: lgr,
			DB:     db,
		})
	if err != nil {
		log.Fatal(err)
	}
	done, err := repo.Start(ctx)
	if err != nil {
		log.Fatal(err)
	}
	return repo, done
}

func getDb() *mongo.Database {
	ctx, cnl := context.WithTimeout(context.Background(), 5*time.Second)
	defer cnl()
	// use test name as db name to parallel tests.
	opts := options.Client()
	opts.ApplyURI("mongodb://localhost:27017/?connect=direct")
	client, err := mongo.Connect(ctx, opts)
	if err != nil {
		panic(fmt.Errorf("run `make dependencies_up` before, error: %w", err))
	}
	err = client.Ping(ctx, nil)
	if err != nil {
		panic(fmt.Errorf("error %v\nrun `make dependencies_up` before running main\n", err))
	}
	return client.Database("test")
}
