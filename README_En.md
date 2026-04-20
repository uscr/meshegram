# Meshegram

[На русском](README.md)

A two-way Meshtastic ↔ Telegram bridge in a single small Go service:

- text messages from the mesh are forwarded into a Telegram chat (channel or group) with author, channel name, TTL and signal quality;
- tapback reactions in the mesh are appended as a `reactions: …` line to the original forward via message edit (native Telegram reactions aren't used — a bot can only set one reaction per message and each new one would overwrite the previous);
- whitelisted users in the same chat can reply back into the mesh with `/send`, or list the node's channels with `/channels`.

One process, one TCP connection to the node, one Telegram bot. Configured entirely through environment variables.

Contact the author: [@uscr0](https://t.me/uscr0)

## What it looks like

Incoming from the mesh:

```
📡 home · #LongFast
👤 Denis (DEN) !43a1b2c0
2 хопа · SNR -5.2 dB · RSSI -104 dBm

> Hello from the mesh!
```

Replying from the chat:

```
/send Hello back
```

Or to a different channel:

```
/send #admin how's the node?
```

## Environment variables

| Variable | Required | Description |
|---|---|---|
| `MESHEGRAM_TG_TOKEN` | yes | Telegram bot token |
| `MESHEGRAM_TG_CHAT` | yes | Chat ID — channel or group (e.g. `-1001234567890`) |
| `MESHEGRAM_NODE` | yes | Meshtastic node address (`host` or `host:port`, port defaults to 4403) |
| `MESHEGRAM_ALLOWED_USERS` | yes | Comma-separated whitelist. Each entry is either a numeric Telegram ID (`111222333`) or a username (`@uscr0` or plain `uscr0`). The two forms can be mixed |
| `MESHEGRAM_NODE_NAME` | no | Display name of the node in messages (defaults to its address) |
| `MESHEGRAM_CHANNEL` | no | Default channel index for `/send` (0 = primary) |
| `MESHEGRAM_HOP_LIMIT` | no | Hop limit for outgoing packets (default `3`) |
| `MESHEGRAM_TG_PROXY` | no | Proxy for the Telegram API (`http://`, `https://` or `socks5://host:port`) |
| `MESHEGRAM_RECONNECT_INTERVAL` | no | Delay before reconnecting to the node (default `10s`) |
| `MESHEGRAM_PREPEND_AUTHOR` | no | Prepend the author name to the mesh payload (`true`/`false`, default `true`) |
| `MESHEGRAM_ONLY_CHANNEL` | no | Comma-separated channel allowlist (e.g. `LongFast,admin`). When set, only messages and reactions from the listed channels are forwarded. Case-insensitive; the leading `#` is optional |
| `MESHEGRAM_IGNORE_CHANNEL` | no | Comma-separated channel blocklist. Applied on top of `MESHEGRAM_ONLY_CHANNEL` |
| `MESHEGRAM_ONLY_MESSAGE_REGEXP` | no | Go [`regexp`](https://pkg.go.dev/regexp/syntax) pattern. When set, only messages whose text matches are forwarded |
| `MESHEGRAM_IGNORE_MESSAGE_REGEXP` | no | Regexp pattern. Messages whose text matches are dropped. Applied on top of `MESHEGRAM_ONLY_MESSAGE_REGEXP` |

Any of the four variables may be empty or unset, meaning "no filter". Channel filters also apply to reactions; message-regexp filters only apply to plain messages.

## Commands

| Command | Context | Effect |
|---|---|---|
| `/send <text>` | DM | Send to the default channel (`MESHEGRAM_CHANNEL`) |
| `/send <text>` | group | Send to the channel of the latest incoming mesh message (falls back to the default) |
| `/send #name <text>` | DM and group | Send to channel named `name` (case-insensitive) |
| `/channels` | DM and group | List the channels the node is aware of |

In DM the `/send` prefix can be dropped — any plain text goes to the default channel.

Users outside `MESHEGRAM_ALLOWED_USERS`:

- in DM get an error message with their Telegram ID (handy for onboarding — new user texts the bot, admin adds the ID to the whitelist);
- in a group are silently ignored.

⚠️ Telegram usernames can be changed; numeric IDs cannot. For critical users that might re-handle themselves one day, prefer whitelisting by ID.

ℹ️ Group admins with "Remain anonymous" enabled arrive as `From = GroupAnonymousBot` — Telegram hides their real identity. Meshegram allows such senders automatically when the message is in the bridged chat (`MESHEGRAM_TG_CHAT`) — by definition the sender is already an admin of that group. The mesh payload then carries the group's title in place of an author.

## Docker

```bash
docker run -d --name meshegram --restart=unless-stopped --network=host \
  -e MESHEGRAM_TG_TOKEN=123:ABC \
  -e MESHEGRAM_TG_CHAT=-1001234567890 \
  -e MESHEGRAM_NODE=192.168.10.2:4403 \
  -e MESHEGRAM_NODE_NAME=home \
  -e MESHEGRAM_ALLOWED_USERS="@uscr0,123456789" \
  -e MESHEGRAM_CHANNEL=0 \
  meshegram:latest
```

The bot must be added to the target chat:

- in a **channel** — as an admin with "Post messages" permission;
- in a **group/supergroup** — a regular member is enough; commands work thanks to Telegram's privacy mode.

## Proxy

Telegram API is often unreachable directly. `MESHEGRAM_TG_PROXY` supports `http://`, `https://`, `socks5://` schemes (with optional `user:pass@host:port` auth). When unset, standard `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` env vars are honoured. The Meshtastic TCP connection is direct (not proxied).

## Building the container

Multi-arch (`linux/amd64` + `linux/arm64`) from one Dockerfile:

```bash
docker buildx create --use --name meshegram 2>/dev/null || true

docker buildx build --platform linux/amd64,linux/arm64 \
  -t ghcr.io/uscr0/meshegram:latest \
  --push .
```

Replace `ghcr.io/uscr0` with your registry.

## Building locally

```bash
go build ./cmd/meshegram
```

## Preparing a Meshtastic node

- `Network` → `Network Enabled = true`
- `Network` → `WiFi Mode = Client` (or Ethernet on devices that support it).

Meshtastic allows **one** TCP client on its Network API at a time. If another client (mobile app, web client, another bot) is connected, the handshake will hang — meshegram detects that, logs it, and retries once the node is free.

## License

GPL-3.0, see [LICENCE](LICENCE).
