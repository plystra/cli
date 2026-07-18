package newproject

const goModuleTemplate = `module %s

go 1.26

require github.com/plystra/kernel %s
`

const plystraTemplate = `http:
  address: ":8080"
  expose: []

timeouts:
  startup: 2m

capabilities:
  require: []
  use: {}
  aliases: {}

config: {}
`

const readmeTemplate = `# %s

This is the runnable Plystra Go Module ` + "`%s`" + `.

Local plugins belong in direct child directories containing ` + "`plugin.yaml`" + `. Do not add a root ` + "`plugins/`" + ` container.

## Development

` + "```powershell" + `
plystra plugin create records
plystra capability create records.read --plugin records --expose
plystra generate
plystra generate --check
go test ./...
go vet ./...
` + "```" + `

Mutating Plystra commands regenerate automatically. Run ` + "`plystra generate`" + ` after manual declaration edits and use ` + "`plystra generate --check`" + ` as the read-only consistency gate.

Generated source under ` + "`generated/`" + ` is owned by the Plystra CLI. Do not edit it manually; commit it to Git.
`

const libraryReadmeTemplate = `# %s

This is the non-runnable Plystra plugin Go Module ` + "`%s`" + `.

Local plugins belong in direct child directories containing ` + "`plugin.yaml`" + `. Do not add a root ` + "`plugins/`" + ` container.

## Development

` + "```powershell" + `
plystra plugin create records
plystra capability create records.read --plugin records
plystra generate
plystra generate --check
go test ./...
go vet ./...
` + "```" + `

Mutating Plystra commands regenerate automatically. Run ` + "`plystra generate`" + ` after manual declaration edits and use ` + "`plystra generate --check`" + ` as the read-only consistency gate.

Generated source under ` + "`generated/`" + ` is owned by the Plystra CLI. Do not edit it manually; commit it to Git.
`

const gitignoreTemplate = `/dist/
/generated/sdk/javascript/node_modules/
/generated/sdk/javascript/dist/
.env
.env.local
go.work
go.work.sum
`

const gitattributesTemplate = `* text=auto eol=lf
/generated/** linguist-generated=true
`

const ciTemplate = `name: CI

on:
  push:
  pull_request:

permissions:
  contents: read

jobs:
  test:
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-latest, windows-latest, macos-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v6
        with:
          go-version: "1.26.x"
          cache: true
      - run: go test ./...
      - run: go vet ./...

  race:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v6
        with:
          go-version: "1.26.x"
          cache: true
      - run: go test -race ./...
`
