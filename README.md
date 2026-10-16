# abp

Smart playlists for [Audiobookshelf](https://www.audiobookshelf.org) podcasts, with a TUI.

Pick some podcasts, set a size, and `abp` keeps one playlist filled with the **oldest unfinished
episodes** across them, in publish order. When you finish an episode it drops out and the next
oldest flows in. The playlist is **updated in place** (never deleted and recreated).

## Install

    make tidy        # first time only: generates go.sum
    make build       # ./abp
    make install     # to ~/.local/bin (PREFIX=/usr/local to change)

Run `abp` for the TUI. First launch asks for your server URL and API key.

## Development

`make help` lists everything. The usual ones: `make test`, `make test-race`, `make cover`,
`make fmt`, `make vet`, `make lint` (needs golangci-lint), `make check` (fmt-check + vet + test),
`make dist` (cross-compiled tarballs for linux/darwin, amd64/arm64) and `make clean`.

## Where things live

| What | Where |
|---|---|
| Playlists + podcasts (human-readable YAML) | `~/.config/abp/config.yaml` |
| Server URL + API key (mode 0600, dir 0700) | `~/.local/share/abp/credentials.json` |
| Sync bookkeeping (ABS playlist ids, last result) | `~/.local/share/abp/state.json` |
| Cron output | `~/.local/share/abp/abp.log` |

Override with `ABP_CONFIG`, `ABP_DATA_DIR`, or `ABP_URL` / `ABP_API_KEY`.

I deliberately used a private 0600 file rather than the OS keyring: on Linux the keyring is usually
unreachable from cron, which would break scheduled syncs.

## config.yaml

    playlists:
      - id: history-backlog        # stable slug; used by `abp sync` and the crontab
        name: History Backlog
        description: Slow-burn history, oldest first   # optional, shown in Audiobookshelf
        library: li_abc123
        library_name: Podcasts
        limit: 25                  # max episodes in the playlist
        schedule: "*/30 * * * *"   # empty = manual only
        podcasts:
          - id: li_def456
            title: Revolutions

## CLI (for cron)

    abp sync history-backlog       # sync one (or several) playlists
    abp sync -all                  # sync everything
    abp sync -n -all               # dry run
    abp list
    abp cron install               # crontab := schedules in config.yaml
    abp cron remove
    abp cron show

abp only touches crontab lines ending in `# abp:<id>`; the rest of your crontab is left alone.
Editing a schedule in the TUI updates the crontab automatically.

## TUI keys

- Playlists: `enter` open · `n` new · `d` delete · `s` sync · `S` sync all · `C` re-install crontab · `o` server settings
- Playlist: `a` add podcasts · `x` remove · `e` edit · `c` schedule · `p` preview queue · `s` sync · `D` delete
- Podcast picker: type to filter · `tab`/`enter` toggle a podcast on/off (saved immediately, you stay on the screen) · `esc` done

## Troubleshooting

- `abp.log` (in the data dir) only contains output from cron runs. Run `abp sync <id>` by hand to see output live.
- After each update abp re-reads the playlist and checks it. If it differs, abp writes
  `~/.local/share/abp/mismatch-<id>.txt` (expected vs. what the server returned, position by position).
  Missing/extra episodes are an error; the same episodes in a different order is a note.
- These checks are made by abp, so they won't appear in Audiobookshelf's own logs.

## Notes

- Audiobookshelf's add/remove endpoints leave the playlist numbering untidy (removing leaves gaps; new
  items are numbered "count + 1", which puts them mid-list after a removal). abp therefore adds what's
  missing, removes what's unwanted, then sends one reorder request that renumbers the whole playlist.
  Playlists scrambled by earlier versions are repaired on the next sync.
- Audiobookshelf ignores an empty description on update, so clearing a description in abp won't clear it
  on the server (set it to something else, or clear it in Audiobookshelf).
- abp owns each playlist's name and description: they're pushed to Audiobookshelf on sync, so edits made to them directly in Audiobookshelf will be overwritten.
- A podcast that fails to load aborts the sync (the playlist is left as-is) rather than shrinking it;
  a podcast deleted from Audiobookshelf is skipped with a warning.
- Audiobookshelf removes a playlist when its last item is removed, so if everything is finished the
  playlist disappears and is recreated when new episodes arrive.
- Playlists are tied to one library; a playlist's library can't change after creation.
