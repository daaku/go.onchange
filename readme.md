go.onchange
===========

Build and start a package named by it's import path, and monitor it
and it's dependencies for changes and recompile and restart the server
as necessary.

Installation:

```sh
go install github.com/daaku/go.onchange
```

For example, to run the command contained in `github.com/daaku/rell`:

```sh
go get github.com/daaku/rell
go.onchange github.com/daaku/rell
```
