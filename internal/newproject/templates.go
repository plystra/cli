package newproject

const goModuleTemplate = `module %s

go 1.26

require github.com/plystra/kernel %s
`

const plystraTemplate = `instance_id: %s

http:
  address: ":8080"
  expose: []

database:
  dsn_file: /run/secrets/plystra_database_dsn

timeouts:
  startup: 2m
  audit_write: 5s

capabilities:
  require: []
  use: {}

config: {}
`

const readmeTemplate = `# %s

This is the runnable Plystra Go Module ` + "`%s`" + `.

Local plugins belong in direct child directories containing ` + "`plugin.yaml`" + `. Do not add a root ` + "`plugins/`" + ` container.

## Development

` + "```powershell" + `
plystra dev
plystra test
plystra build
` + "```" + `

Generated source under ` + "`generated/`" + ` is owned by the Plystra CLI and committed to Git.
`

const libraryReadmeTemplate = `# %s

This is the non-runnable Plystra plugin Go Module ` + "`%s`" + `.

Local plugins belong in direct child directories containing ` + "`plugin.yaml`" + `. Do not add a root ` + "`plugins/`" + ` container. Development commands create a temporary runnable host when one is required.

## Development

` + "```powershell" + `
plystra dev
plystra test
plystra build
` + "```" + `

Generated source under ` + "`generated/`" + ` is owned by the Plystra CLI and committed to Git.
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
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v6
        with:
          go-version: "1.26.x"
          cache: true
      - run: go test ./...
      - run: go vet ./...

  race:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v6
        with:
          go-version: "1.26.x"
          cache: true
      - run: go test -race ./...
`
