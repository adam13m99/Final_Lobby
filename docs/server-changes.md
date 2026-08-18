# Changes made to the shared server

**Host:** `87.107.110.199` (MobinHost), 4 vCPU, 8 GB RAM, Ubuntu.

This machine runs an unrelated, live SNI-proxy business with real paying
users: nginx on **TCP** 443, CoreDNS on 53, and an active WireGuard peer.
Final Lobby is a guest on it for development and measurement only.

Every change we have made is listed below, with what it was before and how to
undo it. Nothing outside this list has been touched. **nginx, CoreDNS,
WireGuard and their configuration files were not modified, restarted, or read
for anything other than confirming they still work.**

Last audited: 2026-08-18.

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

| Rule | Owner |
|---|---|
| `22/tcp` | pre-existing (SSH) |
| `443/udp` | **ours** |

**To undo:** `ufw delete allow 443/udp`

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
| `/etc/systemd/system/relay.service` | The service definition |

`/etc/finallobby` is mode 750, root:finallobby.

**Do not regenerate `relay.key`.** Every client will have the matching public
key built into it; replacing the key locks all of them out until they are
rebuilt and redistributed.

**To undo:** `rm -rf /opt/finallobby /etc/finallobby /etc/systemd/system/relay.service`

---

## 5. A systemd service

`relay.service`, enabled and running. It binds **UDP 443 only**.

The unit is deliberately locked down: no new privileges, private `/tmp`,
read-only system paths, no kernel-module or cgroup access, and the only
capability it holds is `CAP_NET_BIND_SERVICE` — the minimum needed for an
unprivileged user to hold a port below 1024.

**To undo:**

```
systemctl disable --now relay.service
rm /etc/systemd/system/relay.service
systemctl daemon-reload
```

---

## 6. Temporary things, already cleaned up

These existed during testing and have been removed:

- `/tmp/wintun.zip` and `/tmp/wintun/` — the Wintun driver, downloaded
  through this server because `wintun.net` is unreachable from Iran
- `/tmp/lt-*.log`, `/tmp/x.log`, `/tmp/devrelay.log` — load-test output
- The `4443/udp` firewall rule
- Extra relay processes started on ports 4443 and 9443 for testing

Nothing from testing is still running. The only Final Lobby process on the
box is the one systemd service.

---

## Complete removal

To take Final Lobby off this machine entirely:

```bash
systemctl disable --now relay.service
rm -f /etc/systemd/system/relay.service
systemctl daemon-reload
rm -rf /opt/finallobby /etc/finallobby
rm -f /etc/sysctl.d/99-finallobby.conf
ufw delete allow 443/udp
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
