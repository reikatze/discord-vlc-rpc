# Tests

Run these commands from the Source root:

```sh
go run ./cmd/test
go run ./cmd/test -race -count=1 ./...
go run ./cmd/test vet
go run ./cmd/test -run TestGeneratedTrayIcon ./modules
```

The test files retain `package modules` and their platform build constraints.
The runner uses Go's `-overlay` option to load them into `modules/` for compilation,
without copying files there or changing production visibility. It deletes its
temporary overlay after the command finishes and propagates failures to CI.

The nested `go.mod` separates these test sources from the application's package
walk. Use the runner above; running `go test ./...` directly will not execute
this suite. Additional Go test flags, package patterns, and benchmarks are passed
through to the Go command.
