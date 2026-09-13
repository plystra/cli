# Verify, build, and release

Use the narrowest relevant authored test first, then verify the complete selected Project state:

    plystra generate --check
    plystra check
    go test ./...
    go build ./...
    go vet ./...
    go mod verify

Use `GOWORK=off` when proving that the module resolves and builds independently of a local workspace. When a generated JavaScript SDK exists, run its declared install, typecheck, build, runtime-test, declaration, and dry-run package commands from `generated/sdk/javascript`.

Generated source and compatibility records are reviewable outputs, but they are never edited manually. Change authored Go, YAML, or module inputs and rerun `plystra generate`.

The installed CLI `0.1.0` does not expose the planned public `plystra dev`, `plystra build`, release-preparation, or publication operations. Do not present local checks as proof that an unavailable release workflow ran.

See the [Project README](../../../../README.md) for the exact generated application and JavaScript checks supported by this Project.
