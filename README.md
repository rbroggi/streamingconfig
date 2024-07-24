# Streaming configuration

![](doc/img.png)

Streamingconfig is a [MongoDB Change Streams](https://www.mongodb.com/docs/manual/changeStreams/) powered, hot-reloadable configuration for Go!

This library enables real-time configuration updates in your distributed Go services, eliminating the need for restarts.

Benefits:

* **Dynamic Feature Flags**: Activate or deactivate features on the fly.
* **Zero-Downtime Config Reloads**: Update configurations without service interruptions.

Ideal for:

* **Kubernetes Deployments**: Ensures all pods receive configuration changes seamlessly.

A streamingconfig is a simple Go struct that:
* contains json and default field tags;
* implements a simple single interface:
```go
type Config interface {
    Update(new Config) error
}
```

## Design

* **Auditable**: All configuration changes are tracked for historical reference. 
This comes at the cost of increased storage consumption, as each change creates a 
new document containing all configuration fields.
* **Immutable**: Configurations are versioned and immutable. Every update creates 
a new version, preserving the history.
* **Eventually Consistent**: Configuration changes eventually replicate to other local 
repositories. There may be a slight delay.
* **Dynamic Defaults**: Default values are not stored and can be modified during 
deployment of new configuration versions.
* **Fast Local Retrieval**: Getting configuration data locally is fast as it's retrieved 
from memory, not requiring remote queries.
* **Input validation**: User-provided configuration changes validation through the `Update` method.

## Usage

checkout the [example](./example/server/main.go) folder for a more real-world scenario. 

As a library user, you will have to:

1. Define a configuration with `json` field tags (and optionally with `default` field tags);
2. Make sure that your configuration type implements the `streamingconfig.Config` interface;
    > **_NOTE:_**  Within the `Update` method you can implement configuration validation see example below.
3. Instantiate and start the repository and use it;

```go
package main

import (
	"errors"
	config "github.com/rbroggi/streamingconfig"
)

type conf struct {
	Name    string   `json:"name" default:"john"`
	Age     int      `json:"age"`
}

func (c *conf) Update(new config.Config) error {
	newCfg, ok := new.(*conf)
	if !ok {
		return errors.New("wrong configuration")
	}
	c.Name = newCfg.Name
	c.Age = newCfg.Age
	return c.validate()
}

// validation should not disallow zero-values as the `Update` 
// method is called on the struct without it's default values.
func (c *conf) validate() error {
	if c.Age < 0 {
		return errors.New("age must not be negative")
	}
	return nil
}

func main() {
	repo, err := config.NewWatchedRepo[*conf](
		config.Args{
			Logger: getLogger(),
			DB:     getDatabase(),
		})
	if err != nil {
		log.Fatal(err)
	}
	ctx, cnl := context.WithCancel(context.Background())
	done, err := repo.Start(ctx)
	if err != nil {
		log.Fatal(err)
	}
	// use repo
	cnl()
	<-done
}
```

## Test

```shell
make dependencies_up
make tests
```

### Run example 

Start by starting the dependencies (mongo), and run the example-server which is a simple HTTP server to receive GET,POST requests for reading/updating configurations.
```shell
make dependencies_up
make run-example-server
```
on a different shell, run the sample APP, which is an app that receives changes upon changes that happen in the configuration through the server API endpoints.

```shell
make run-example-app
```

#### Getting latest configuration request
```shell
curl -X GET --location "http://localhost:8080/configs/latest"
```
#### Changing latest configuration request
```shell
curl -X PUT --location "http://localhost:8080/configs/latest" \
    -H "user-id: mark" \
    -d '{
  "name": "betty",
  "age": 35
}'
```
#### Listing multiple versions
```shell
curl -X GET --location "http://localhost:8080/configs?fromVersion=0&toVersion=21"
```
