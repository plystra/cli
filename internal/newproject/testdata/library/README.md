# email

This is the non-runnable Plystra plugin Go Module `example.com/acme/email`.

Local plugins belong in direct child directories containing `plugin.yaml`. Do not add a root `plugins/` container. Development commands create a temporary runnable host when one is required.

## Development

```powershell
plystra dev
plystra test
plystra build
```

Generated source under `generated/` is owned by the Plystra CLI and committed to Git.
