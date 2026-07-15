# my-app

This is the runnable Plystra Go Module `example.com/acme/my-app`.

Local plugins belong in direct child directories containing `plugin.yaml`. Do not add a root `plugins/` container.

## Development

```powershell
plystra dev
plystra test
plystra build
```

Generated source under `generated/` is owned by the Plystra CLI and committed to Git.
