# Load test harness

Synthetic peers that complete real Noise handshakes and exchange real
encrypted datagrams through a relay. What it measures is the relay's actual
work, not a simplified stand-in.

```
loadtest -relay 127.0.0.1:9443 -relay-pub <hex> \
         -peers 1500 -pps 60 -packet-size 200 -duration 60s -ramp-up 15s
```

Peers are laid out ten to a room, addressed exactly as `coordinator/internal/ipam`
would address them, and each sends to the next slot around its own room — so
every peer both sends and receives, as in a real match. Each packet carries a
nanosecond timestamp, so latency is measured end to end through the relay.

The relay must be started with `-dev-unsigned-tickets` for the harness to
connect; tickets are of the form `roomID|virtualIP`.

## Results — 2026-08-18, MobinHost dev server

4 vCPU, 8 GB. **The load generator ran on the same box as the relay**, which
matters: at high packet rates the two compete for the same four cores, so
these figures are a floor, not the relay's ceiling.

| Peers | pps each | Offered | Connected | Loss | p50 | p99 | Relay CPU |
|---|---|---|---|---|---|---|---|
| 200 | 60 | 12k pps | 200/200 | 0.2% | 1.4 ms | 55 ms | 96% |
| **1500** | **12** | **18k pps** | **1500/1500** | **0.000%** | **613 µs** | **96 ms** | **118%** |
| 500 | 60 | 30k pps | 500/500 | 5.4% | 16 ms | 1.0 s | 139% |
| 800 | 60 | 48k pps | 741/800 | 27% | 578 ms | 4.4 s | 149% |
| 300 | 300 | 90k pps | 272/300 | 47% | 782 ms | 2.0 s | 150% |

### What this establishes

**Peer count is not a limit.** 1500 concurrent sessions ran with *zero* lost
packets, zero routing drops, zero queue drops, and sub-millisecond median
latency. Every one of the 1500 handshakes succeeded. The ancestor platform's
failure — degrading as players joined — is gone, and this is the measurement
that says so.

**Packet rate is the limit.** Compare rows 2 and 5: 1500 peers at a low rate
is flawless, while 300 peers at a high rate loses half its packets. The relay
does not care how many players there are; it cares how many packets per
second arrive.

The knee on this contended box sits somewhere between 18k and 30k packets per
second. `dropped_queue` in the relay's own counters is what goes wrong first:
the per-peer writer goroutines cannot drain their queues fast enough, because
every packet costs one `sendto` syscall plus a ChaCha20 seal.

### What it does not establish

The 1500-player question is **not yet answered**, because the generator was
competing for the same CPUs. A ten-player Dota match at 30 Hz puts roughly
540 packets per second through the relay, so 1500 players is on the order of
80k pps — above where this contended box degrades. Two things are needed
before that number can be trusted:

1. A second machine to generate load, so the relay gets the whole box.
2. Batched syscalls (`recvmmsg`/`sendmmsg` via `golang.org/x/net/ipv4`
   `ReadBatch`/`WriteBatch`), which amortise the per-packet syscall over up
   to 64 packets. This is the standard fix for exactly this ceiling and is
   the single highest-value optimisation left in the data plane.

### Fixes this exercise already produced

- **Parallel socket readers.** The relay read from its socket on one
  goroutine, capping it near one core. It now reads on one goroutine per CPU;
  concurrent reads on a single UDP socket are safe.
- **Socket buffers.** The kernel default of 208 KB holds about ten
  milliseconds of traffic at load, and `netstat -su` showed over a million
  receive-buffer errors. The relay now requests 8 MB and warns if the kernel
  caps it. `net.core.rmem_max` was raised to 16 MB on the server; only the
  ceiling changed, so no other service's behaviour did.
- **Packet counters.** The relay reports handshakes, forwarded, auth
  failures, routing drops and queue drops. Before these, loss could only be
  guessed at; with them, the queue-drop counter identified the bottleneck
  immediately.
