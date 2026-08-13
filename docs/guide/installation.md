# Installation

Kranz supports macOS and Linux on x86-64 and ARM64. Windows is not currently
supported because process-group and listener inspection use Unix facilities.

## Homebrew

```bash
brew install kranz-org/tap/kranz
kranz --version
```

Homebrew installs a published release archive and verifies its checksum.

## Go install

With Go 1.24 or newer:

```bash
go install github.com/kranz-org/kranz/cmd/kranz@latest
```

## GitHub releases

Download an archive from [GitHub Releases](https://github.com/kranz-org/kranz/releases),
verify it against `checksums.txt`, and put `kranz` on your `PATH`.

## Build from source

```bash
git clone https://github.com/kranz-org/kranz.git
cd kranz
make build
./bin/kranz
```

`make install` installs the current checkout into `GOBIN` or `GOPATH/bin`.
