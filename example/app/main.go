package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	config "github.com/rbroggi/streamingconfig"
	appcfg "github.com/rbroggi/streamingconfig/example/config"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func main() {
	runnableCtx, cancelRunnables := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancelRunnables()
	_, done := initRepo(runnableCtx)
	// until shutdown signal is sent
	<-runnableCtx.Done()
	<-done
}

func initRepo(ctx context.Context) (*config.WatchedRepo[*appcfg.Conf], <-chan struct{}) {
	lgr := slog.Default()
	db := getDb()
	repo, err := config.NewWatchedRepo[*appcfg.Conf](
		config.Args{
			Logger: lgr,
			DB:     db,
		}, config.WithOnUpdate[*appcfg.Conf](func(conf *appcfg.Conf) {
			lgr.With("cfg", conf).Debug("config updated")
			slog.SetLogLoggerLevel(conf.LogLevel)
		}))
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
