# Installation

Kranz supports macOS and Linux on x86-64 and ARM64. Windows is not supported
natively, because process-group and listener inspection use Unix facilities;
see [WSL 2](#windows-wsl-2) for the supported way to run it on Windows.

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

## Debian and Ubuntu

```bash
VERSION=0.12.0
ARCH=amd64  # use arm64 on an ARM machine
curl -fLO "https://github.com/kranz-org/kranz/releases/download/v${VERSION}/kranz_${VERSION}_linux_${ARCH}.deb"
sudo apt install "./kranz_${VERSION}_linux_${ARCH}.deb"
kranz --version
```

Change `VERSION` when installing another release. The package installs the
binary into `/usr/bin`, shell completions for bash, zsh, and fish, and the
documentation into `/usr/share/doc/kranz`.

Remove it with `sudo apt remove kranz`.

## Fedora, RHEL, and Rocky

```bash
VERSION=0.12.0
ARCH=amd64  # use arm64 on an ARM machine
sudo dnf install "https://github.com/kranz-org/kranz/releases/download/v${VERSION}/kranz_${VERSION}_linux_${ARCH}.rpm"
kranz --version
```

Remove it with `sudo dnf remove kranz`.

The packages define no system account, no unit file, and start no service on
install. Kranz supervises processes owned by whoever invokes it and keeps its
runtime state under that user's directories, so there is nothing system-wide to
configure afterwards.

## Windows (WSL 2)

Kranz runs under WSL 2 as an ordinary Linux install. Inside your distribution,
use the Debian or Fedora instructions above, or `go install`.

Two things are worth knowing:

- **Keep the project on the Linux filesystem.** A project under `/mnt/c` is
  reached through a translation layer, where file watching is unreliable and
  every process start is slower. Configuration reload depends on watching, so a
  project under `$HOME` behaves correctly and one under `/mnt/c` may not
  notice edits.
- **Ports are shared with Windows.** WSL 2 forwards listening ports to the
  Windows host, so `kranz ports` and the port conflict checks see the same
  numbers your browser does. A port held by a Windows process appears as taken.

A runtime started in one WSL terminal is visible from another WSL terminal of
the same distribution, because both share the user's runtime directory. It is
not visible from Windows itself, and not from a different distribution.

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

`make install` installs the current checkout into `GOBIN` or `GOPATH/bin`, and
`make dev` builds `bin/kranz-dev` so you can exercise a checkout without
replacing an installed `kranz`.

## Shell completion

The Linux packages install completions already. For any other installation:

```bash
kranz completion bash > /usr/share/bash-completion/completions/kranz
kranz completion zsh  > "${fpath[1]}/_kranz"
kranz completion fish > ~/.config/fish/completions/kranz.fish
```

Completion covers commands, subcommands, and each command's own options,
including the values of options that take a fixed set.
