# Contributing

## Development Requirements

* Go
* `gcloud`
* `mutagen`

## Build

```bash
go build -o ./bin/isotty ./cmd/isotty
```

## Test

```bash
go test ./...
```

## Dependency Check

```bash
./bin/isotty version
```
