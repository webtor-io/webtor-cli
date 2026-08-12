# webtor-cli

Command-line client for [webtor.io](https://webtor.io): store torrents,
stream or download their content over plain HTTP, manage your library and
long-term Vault storage — from the terminal or from scripts.

```console
$ webtor add "magnet:?xt=urn:btih:08ada5a7a6183aae1e09d831df6748d566095a10"
id     08ada5a7a6183aae1e09d831df6748d566095a10
name   Sintel
size   129.2 MB
files  11

$ webtor ls 08ada5a7a6183aae1e09d831df6748d566095a10
$ webtor download 08ada5a7a6183aae1e09d831df6748d566095a10 Sintel.mp4
$ webtor play "magnet:?xt=urn:btih:..."          # straight into VLC
$ webtor url 08ada5a7a6183aae1e09d831df6748d566095a10 Sintel.mp4 | xargs mpv
```

Torrents, magnets and infohashes pipe straight in — `add` takes the payload,
every other command takes the resource id:

```console
$ cat file.torrent | webtor add
$ curl -sL https://example.com/file.torrent | webtor add
$ echo 08ada5a7a6183aae1e09d831df6748d566095a10 | webtor play
$ some-tool | webtor download --stdout Sintel.mp4 > out.mp4
```

## Install

**Homebrew** (macOS / Linux):

```console
$ brew install webtor-io/tap/webtor
```

**Prebuilt binaries** — grab the archive for your OS/arch from the
[latest release](https://github.com/webtor-io/webtor-cli/releases/latest)
(darwin / linux / windows, amd64 + arm64), unpack and put `webtor` on your
`PATH`:

```console
$ curl -sL https://github.com/webtor-io/webtor-cli/releases/download/v1.0.0/webtor_1.0.0_linux_amd64.tar.gz | tar xz webtor
$ sudo install webtor /usr/local/bin/
```

**Docker** — the image pairs naturally with the config-less env mode:

```console
$ docker run --rm -e WEBTOR_BACKEND=webui -e WEBTOR_API_KEY=... \
    ghcr.io/webtor-io/webtor-cli:latest info 08ada5a7a6183aae1e09d831df6748d566095a10
```

**From source** (Go 1.25+; note the binary lands as `webtor-cli`):

```console
$ go install github.com/webtor-io/webtor-cli@latest
```

## First run

`webtor` (or `webtor config init`) walks you through the setup:

1. **webtor.io account** *(recommended)* — logs the terminal in like `gh`:
   a one-time code, a browser confirmation on webtor.io, and the CLI gets
   its own revocable API key. Requires a [paid plan](https://webtor.io/donate).
2. **RapidAPI** — paste your key from the
   [webtor listing](https://rapidapi.com/webtor/api/torrents-api).
3. **Self-hosted** — point it at your own
   [rest-api](https://github.com/webtor-io/self-hosted) base URL.

Configuration lives in `~/.config/webtor/config.yaml`. API keys go to the OS
keyring (macOS Keychain, Windows Credential Manager, Linux Secret Service);
where no keyring is available they fall back to `credentials.yaml` (0600).
`WEBTOR_NO_KEYRING=1` forces the file. Multiple named contexts are supported:
`--context`, `webtor config use <name>`. CI needs no files at all:

```console
$ WEBTOR_BACKEND=webui WEBTOR_API_KEY=... webtor --json info <hash>
```

The `WEBTOR_*` variables configure this config-less mode as a set — they are
only read when `WEBTOR_BACKEND` is set, and never override file contexts.

## Commands

| | |
|---|---|
| `auth login` / `status` / `logout` | device-flow login, account info |
| `add` | store a magnet / infohash / `.torrent` / stdin |
| `info`, `ls` | inspect a stored torrent (`--tree`, `--all`, `--json`) |
| `download` | whole torrent / directories file-by-file with the torrent's layout preserved; single files by id/index/path; resume; `--stdout` |
| `export`, `url` | resolve the short-lived export URLs / print the download URL |
| `play` | stream into a media player (`--player vlc` default, mpv/iina work too); picks the biggest video automatically, accepts a raw magnet |
| `library ls/add/rm/rename` | the account library (webtor.io accounts); bare `webtor library` on a terminal opens an interactive browser (play/download/rename/remove) |
| `vault status/pledge/unpledge` | long-term storage; `pledge --wait` polls to completion; bare `webtor vault` on a terminal browses pledges interactively |
| `profile` | account profile |
| `config init/show/use` | contexts |

**Interactive mode** (terminal only — scripts keep the old behavior): the
pickers are arrow-key lists — ↑↓/jk move, typing filters live, Enter
selects, Tab marks in multi-select, Esc cancels. `play` asks which file
when a torrent has several (Enter = biggest video), `download -i` picks
files from the list, bare `webtor library` / `webtor vault` browse the
account. Piped answers fall back to a numbered prompt automatically;
`WEBTOR_PLAIN_PICKER=1` forces it. Aliases: `dl` = download, `p` = play,
`lib` = library, `v` = vault, `i` = info, `u` = url.

Every command takes `--json` for machine output; errors then mirror the API's
`{"error":{"code","message"}}` envelope on stderr. Exit codes: `2` usage,
`3` auth, `4` payment required, `5` not found, `6` unsupported on this
backend, `7` rate-limited/timeout.

Built on [`api-sdk-go`](https://github.com/webtor-io/api-sdk-go); the API
reference lives at <https://api.webtor.io/v1/docs/index.html>.

## License

MIT
