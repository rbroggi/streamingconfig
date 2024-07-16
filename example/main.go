package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"streamingconfig"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type config struct {
	Name string `json:"name" default:"john"`
	Age  int    `json:"age"`
}

func (c *config) Update(new streamingconfig.Config) error {
	newCfg, ok := new.(*config)
	if !ok {
		return errors.New("wrong configuration")
	}
	c.Name = newCfg.Name
	c.Age = newCfg.Age
	return nil
}

// issue: `make dependencies_up` before running this executable
func main() {
	lgr := slog.Default()
	db := getDb()
	repo, err := streamingconfig.NewWatchedRepo[*config](
		streamingconfig.Args{
			Logger: lgr,
			DB:     db,
		})
	if err != nil {
		panic(err)
	}
	ctx, cnl := context.WithTimeout(context.Background(), 10*time.Second)
	defer cnl()
	// start repo watching
	done, err := repo.Start(ctx)
	if err != nil {
		panic(err)
	}
	cfg, err := repo.GetConfig()
	if err != nil {
		panic(err)
	}
	// out: {"name":"john","age":0}
	prettyPrintJson(cfg)
	v0, err := repo.GetLatestVersion()
	if err != nil {
		panic(err)
	}
	// out:
	// {"version":0,"updated_by":"","created_at":"0001-01-01T00:00:00Z","config":{"name":"john","age":0}}
	//
	// notice version 0 indicates that the repository was not yet initialized (no
	// update yet called, configuration leverages only default values)
	prettyPrintJson(v0)
	v1, err := repo.UpdateConfig(ctx, streamingconfig.UpdateConfigCmd[*config]{
		By: "user1",
		Config: &config{
			Name: "bobby",
			Age:  30,
		},
	})
	if err != nil {
		panic(err)
	}
	// out: {"version":1,"updated_by":"user1","created_at":"2024-07-16T15:55:40.06717881Z","config":{"name":"bobby","age":30}}
	prettyPrintJson(v1)

	cnl()
	<-done
}

func getDb() *mongo.Database {
	ctx, cnl := context.WithTimeout(context.Background(), 5*time.Second)
	defer cnl()
	// use test name as db name to parallel tests.
	opts := options.Client()
	opts.ApplyURI("mongodb://localhost:27017/?connect=direct")
	client, err := mongo.Connect(ctx, opts)
	if err != nil {
		panic(err)
	}
	return client.Database("test")
}

func prettyPrintJson(cfg any) {
	b, err := json.Marshal(cfg)
	if err != nil {
		panic(err)
	}
	fmt.Printf("%s\n", string(b))
}
