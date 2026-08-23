# Changes made to the shared server

**Host:** `87.107.110.199` (MobinHost), 4 vCPU, 8 GB RAM, Ubuntu.

This machine runs an unrelated, live SNI-proxy business with real paying
users: nginx on **TCP** 443, CoreDNS on 53, and an active WireGuard peer.
Final Lobby is a guest on it for development and measurement only.

Every change we have made is listed below, with what it was before and how to
undo it. Nothing outside this list has been touched. **nginx, CoreDNS,
WireGuard and their configuration files were not modified, restarted, or read
for anything other than confirming they still work.**

Last audited: 2026-08-18 (after coordinator deployment).

---

## 1. Firewall — one port opened

`ufw` was active with a default-deny policy and **only `22/tcp` permitted**.
That is why the relay appeared to be running but nothing could reach it: our
packets arrived at the machine (confirmed with `tcpdump`) and the firewall
discarded them before the relay saw them.

Added:

```
ufw allow 443/udp comment 'Final Lobby relay'
```

UDP 443 and TCP 443 are separate namespaces, so this does not touch nginx.

A second rule for `4443/udp` was added temporarily during a reachability test
and **has already been removed**.

**Current state:**

A third rule was added later for the coordinator's API so both test PCs can
reach it:

```
ufw allow 7001/tcp comment 'Final Lobby coordinator API (test phase)'
```

That API is **not** open to the world: it requires a shared bearer token
(`/etc/finallobby/api.token`). It is a test-phase arrangement and goes away
when the desktop client ships behind TLS and real accounts.

| Rule | Owner |
|---|---|
| `22/tcp` | pre-existing (SSH) |
| `443/udp` | **ours** — relay |
| `7001/tcp` | **ours** — coordinator API, token-gated |

**To undo:** `ufw delete allow 443/udp` and `ufw delete allow 7001/tcp`

---

## 2. Kernel socket buffer ceiling raised

**File added:** `/etc/sysctl.d/99-finallobby.conf`

```
net.core.rmem_max = 16777216
net.core.wmem_max = 16777216
```

Previously both were `212992` (208 KB), which holds roughly **ten
milliseconds** of traffic at game load. `netstat -su` showed **1,084,680
receive-buffer errors** — packets the kernel silently discarded.

This raises only the *maximum* a program is allowed to request. The
per-socket defaults (`net.core.rmem_default`, `net.core.wmem_default`) are
**unchanged at 212992**, so no other service on the box behaves any
differently — a program gets a larger buffer only if it explicitly asks, and
nginx, CoreDNS and WireGuard do not.

**To undo:** `rm /etc/sysctl.d/99-finallobby.conf` then reboot, or
`sysctl -w net.core.rmem_max=212992 net.core.wmem_max=212992`.

---

## 3. A system user

```
useradd --system --no-create-home --shell /usr/sbin/nologin finallobby
```

uid 999, gid 987. No password, no shell, no home directory — it exists only
so the relay does not run as root.

**To undo:** `userdel finallobby`

---

## 4. Files added

| Path | What it is |
|---|---|
| `/opt/finallobby/relay` | The relay binary |
| `/opt/finallobby/relay.test` | A build used for load testing — safe to delete |
| `/opt/finallobby/loadtest` | The load generator — safe to delete |
| `/etc/finallobby/relay.key` | **The relay's private identity key.** Mode 640, root:finallobby |
| `/etc/finallobby/relay.pub` | The matching public key, `1e07798757a7225f04f6bb2a72ed2ab5116c0f2d7d3ffefd6db96fa4e85bf72e` |
| `/opt/finallobby/coordinator` | The coordinator binary |
| `/etc/finallobby/api.token` | Shared bearer token for the player API. Mode 640, root:finallobby |
| `/etc/systemd/system/relay.service` | The relay service definition |
| `/etc/systemd/system/coordinator.service` | The coordinator service definition |

`/etc/finallobby` is mode 750, root:finallobby.

**Do not regenerate `relay.key`.** Every client will have the matching public
key built into it; replacing the key locks all of them out until they are
rebuilt and redistributed.

**To undo:** see *Complete removal* at the end of this document.

---

## 5. Two systemd services

`relay.service` binds **UDP 443 only**. `coordinator.service` binds
**TCP 7001** and depends on nothing else on the box.

Both units are deliberately locked down: no new privileges, private `/tmp`,
read-only system paths, no kernel-module or cgroup access, and neither runs as
root. The only capability either holds is `CAP_NET_BIND_SERVICE` on the relay
— the minimum needed for an unprivileged user to hold a port below 1024.

**To undo:** see *Complete removal* at the end of this document.

---

## 6. Temporary things, already cleaned up

These existed during testing and have been removed:

- `/tmp/wintun.zip` and `/tmp/wintun/` — the Wintun driver, downloaded
  through this server because `wintun.net` is unreachable from Iran
- `/tmp/lt-*.log`, `/tmp/x.log`, `/tmp/devrelay.log` — load-test output
- The `4443/udp` firewall rule
- Extra relay processes started on ports 4443 and 9443 for testing

Nothing from testing is still running. The only Final Lobby processes on the
box are the two systemd services.

---

## 7. A directory for the published installer

Added 2026-08-23.

```
/var/lib/finallobby/dist/          755 root:root
    FinalLobby-Setup.exe           644  the installer players download
    version.json                   644  what build is current, and its hash
/etc/finallobby/download.key       640 root:finallobby
```

`download.key` is the unguessable path segment the download is served under.
A browser cannot send a bearer token, so this is what stands in front of the
file. It is generated once by `scripts/publish.sh` and kept.

The coordinator only reads these; `scripts/publish.sh` writes them over SSH.
The systemd unit gained `-dist-dir` and `-download-key-file`, and
`ReadOnlyPaths=/var/lib/finallobby` so the service cannot write there even if
it wanted to.

**No new listening port and no new firewall rule.** The download is served by
the coordinator on TCP 7001, which was already open.

## Note: what the other project now runs on this box

Surveyed 2026-08-23, read-only, to be sure we do not overlap.

| What | Where | Ours? |
|---|---|---|
| nginx stream SNI router | TCP 443, and 127.0.0.1:9443 | no |
| WireGuard (`wg1`) | UDP 51821 | no |
| CoreDNS | 53, and 127.0.0.1:5353, 9153 | no |
| PostgreSQL | 127.0.0.1:5432 | no |
| `nati-cp-api` control plane | 127.0.0.1:8080 | no |
| `nati-cp-tunnel` reverse SSH to Paris | outbound | no |
| **relay** | **UDP 443** | **yes** |
| **coordinator** | **TCP 7001** | **yes** |

Public TCP 443 is no longer accepted from the internet: `ufw` allows only
22/tcp, 443/udp and 7001/tcp, and that traffic now arrives over WireGuard
instead. Our relay on **UDP** 443 and their nginx on **TCP** 443 are
different sockets and do not interact.

## Complete removal

To take Final Lobby off this machine entirely:

```bash
systemctl disable --now relay.service coordinator.service
rm -f /etc/systemd/system/relay.service /etc/systemd/system/coordinator.service
systemctl daemon-reload
rm -rf /opt/finallobby /etc/finallobby /var/lib/finallobby
rm -f /etc/sysctl.d/99-finallobby.conf
ufw delete allow 443/udp
ufw delete allow 7001/tcp
userdel finallobby
```

The proxy business is untouched by all of it.

---

## Rules we hold ourselves to on this box

- **Never bind TCP 443.** It is nginx's, and it is someone's revenue.
- **Never restart or reconfigure nginx, CoreDNS or WireGuard.** After every
  deployment `scripts/deploy.sh` prints who holds TCP 443, so a mistake is
  visible immediately.
- **Open ports explicitly.** `ufw` denies by default; assume nothing is open.
- **Verify uploads by checksum.** An upload silently failed once because the
  target file was locked by a running process, and the next twenty minutes
  were spent testing an old binary.
- This server is for development and measurement. A dedicated machine gets
  bought before launch.
