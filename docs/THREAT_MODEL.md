# Threat model

tether gives an iPad read access to a personal knowledge vault and lets it add notes to an inbox.
The vault holds private business and personal notes, so the bar is: nobody but its owner reads it,
and nothing reaching tether can write outside the inbox or run commands.

| Threat | Defence | Test |
|---|---|---|
| Someone on the internet reaches tether | It listens only on the tailnet (tsnet); no public port, no port forwarding | by construction |
| Another device or user on the same tailnet | Every request's tailnet login is looked up with WhoIs and checked against `-allow` | `TestOnlyAllowedLoginsGetIn` |
| A web page the owner visits makes the browser POST to tether (CSRF) | Writes need a custom `X-Tether` header (no CORS is ever granted) and a same-host Origin | `TestCaptureNeedsSameOriginWrite` |
| A page name reads files outside `wiki/` (`../`, absolute paths, symlinks) | Strict name pattern, then the resolved path must stay inside `wiki/` | `TestPageReadsOnlyInsideWiki` |
| A capture overwrites a note, or a photo name plants an executable | New files only (`O_EXCL`); the client's file name is discarded except a whitelisted image extension | `TestSaveWritesNewFilesOnly`, `TestSavePhotoKeepsOnlyASafeExtension` |
| A huge upload fills the disk | 25 MB photo cap, request body cap; a partial photo is deleted | `TestSaveRejectsOversizedPhotoAndLeavesNothing` |
| A search query runs shell commands | `exec` with argv and `--`, never a shell; 200-character limit; 10 s timeout | `TestSearchPassesQueryAsArgument` |
| A note in the vault (written from web content by `/learn`) carries a script into the app | Markdown is rendered through DOMPurify; CSP `default-src 'self'` | CSP header in every response |
| The service itself is compromised | The systemd unit makes the filesystem read-only except the inbox, the search index and tether's state | `deploy/tether.service` |

Not covered: a stolen, unlocked iPad that is still on the tailnet (remove the device in the
Tailscale admin console), and anything that can already read the laptop's disk.
