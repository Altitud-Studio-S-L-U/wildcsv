# Contributing

Changes are proposed through pull requests. Fork the repository, create a focused branch, and explain the observable behavior your change adds or fixes. The maintainer controls merges into the protected default branch.

## Local setup

Use a supported Go release (the two latest). The module has no dependencies outside the standard library.

```sh
gofmt -l .
go vet ./...
go test ./...
go build ./cmd/wildcsv
```

Fuzz targets live in `fuzz_test.go`. Run one for a while before proposing a change to the reader, the decoders or detection:

```sh
go test -run '^$' -fuzz '^FuzzReader$' -fuzztime 2m .
```

Every pull request runs the tests with the race detector on Linux, macOS and Windows with both supported Go releases, `staticcheck`, `govulncheck`, short fuzzing runs and a CodeQL scan. Include the results of the checks you ran in your pull request. Releases are published by the maintainer.

## Before opening a pull request

- Read [docs/design.md](docs/design.md). It is the contract: a change in behavior changes the document in the same pull request.
- Add a test for every fixed behavior. Preserve valid tests; do not weaken an assertion to make the suite pass.
- A change to detection reports its effect on the development half of the benchmark (`go run ./internal/cmd/accuracy run --split dev`). The held-out half is run only for a release; see [docs/accuracy.md](docs/accuracy.md).
- Keep the module free of dependencies outside the standard library.
- Keep secrets, customer data and benchmark files out of commits. The benchmark sets are fetched, never committed.

For suspected vulnerabilities, follow [SECURITY.md](SECURITY.md) instead of opening a public issue.
