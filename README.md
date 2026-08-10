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

A `.torrent` can be piped straight in:

```console
$ cat file.torrent | webtor add
$ curl -sL https://example.com/file.torrent | webtor add
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

Configuration lives in `~/.config/webtor/` (`config.yaml` + `credentials.yaml`,
the latter 0600). Multiple named contexts are supported: `--context`,
`webtor config use <name>`. CI needs no files at all:

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
| `download` | files by id/index/path with resume; directories as tar/zip; `--stdout` |
| `export`, `url` | resolve the short-lived export URLs / print the download URL |
| `play` | stream into a media player (`--player vlc` default, mpv/iina work too); picks the biggest video automatically, accepts a raw magnet |
| `library ls/add/rm/rename` | the account library (webtor.io accounts) |
| `vault status/pledge/unpledge` | long-term storage; `pledge --wait` polls to completion |
| `profile` | account profile |
| `config init/show/use` | contexts |

Every command takes `--json` for machine output; errors then mirror the API's
`{"error":{"code","message"}}` envelope on stderr. Exit codes: `2` usage,
`3` auth, `4` payment required, `5` not found, `6` unsupported on this
backend, `7` rate-limited/timeout.

Built on [`api-sdk-go`](https://github.com/webtor-io/api-sdk-go); the API
reference lives at <https://api.webtor.io/v1/docs/index.html>.

## License

MIT
