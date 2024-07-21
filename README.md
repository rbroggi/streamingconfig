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

## Usage

checkout the [example](./example/server/main.go).

1. Define a configuration with `json` field tags (and optionally with `default` field tags):
2. Make sure that your configuration type implements the `streamingconfig.Config` interface:
3. Instantiate and start the repository and use it. 

## Test

```shell
make dependencies_up
make tests
```

### Run example server

```shell
make dependencies_up
make example
```

Optionally, you can also start a second server to check that the changes happening in one server will be reflected in the other:

```shell
HTTP_PORT=8081 make example
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
