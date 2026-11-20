# Quick start

From nothing to a device's syslog in front of an assistant, on one node.

## 1. Install

```
tar xzf logdir_v10.tgz && cd logdir
sudo ./install.sh --add-user "$USER"
```

## 2. Create the gateway

In the web GUI (`https://NODE:11154`, signed in as the user you added): Topology ▸ **＋ New gateway**. The gateway is one shared address (its VIP) that the senders and the assistant use.

Or by hand:

```
sudo cp /usr/local/share/logdir/logdir.conf.example /var/lib/logdir/logdir.conf
sudo chmod 600 /var/lib/logdir/logdir.conf
sudo editor /var/lib/logdir/logdir.conf
```

Set:

- `groups[0].interface` and `vip4`: an unused address on that interface's subnet
- a long random `key`
- `settings.mcp_acl` to the address of the machine the assistant runs on

The daemon picks the file up by itself. Check:

```
logdir --show-gateways
logdir --canvas
```

## 3. List the senders

Configure ▸ **Senders** ▸ right-click ▸ Add row: a name, and the sender's address, a network (`10.9.0.0/16`) or a host name. Syslog is read only from listed senders. The collector listens as soon as there is one.

Then point the device's syslog at the node's address or the gateway address: UDP or TCP 514, or TLS 6514. The **Logs** page (the first entry of the menu) shows messages within seconds.

## 4. Connect an assistant

Install a certificate the client trusts (`logdir --tls-install --cert-file F --key-file F`), then, for Claude Code:

```
claude mcp add --transport http logdir https://VIP/mcp
```

Ask it to `logs_search` for failed logins in the last hour, or which senders have gone quiet.

## 5. More nodes

On a second node, install it the same way. Then run `logdir --cluster-token` on the first node and `logdir --cluster-join CODE` on the second (cluster port 11155). The gateway, the senders and the access lists come across, and both nodes answer on the VIP.

Each node keeps the messages it received; searches ask every node. On links with high latency between nodes, raise the gateway timers (`hello_ms` 500, `hold_ms` 3000) so a slow link does not flip the gateway.
