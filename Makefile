.PHONY: dependencies_down
## dependencies_down: tears down the containerised environment necessary to run component-test against.
dependencies_down:
	docker-compose down

.PHONY: dependencies_up
## dependencies_up: starts the containerised environment necessary to run component-tests against.
dependencies_up: dependencies_down
	docker-compose -f docker-compose.yaml up wait

.PHONY: tests
## tests: runs tests against a locally running mongo container
tests:
	go test -v ./...

.PHONY: build-example
## example: runs an http-server locally
build-example:
	go build -o bin/server github.com/rbroggi/streamingconfig/example/server
	go build -o bin/app github.com/rbroggi/streamingconfig/example/app

.PHONY: run-example-server
## run-example-server: runs an http-server locally
run-example-server: build-example
	./bin/server

.PHONY: run-example-app
## run-example-app: runs an http-server locally
run-example-app: build-example
	./bin/app
