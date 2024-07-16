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