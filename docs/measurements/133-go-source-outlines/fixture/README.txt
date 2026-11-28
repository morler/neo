The Go sources here are stored as *.go.txt so this repository's `go test ./...`
does not try to build them as packages of Neo. run.sh, build-outline.sh and
make-fixture.sh rename them back into a work directory before a run.
