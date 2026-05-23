# V2 to V3 compatibility checklist

This checklist should be completed before V3 replaces MeshedServerTool V2 behavior.

## Storage and import

- [ ] Document V2 platformdirs paths.
- [ ] Document server list persistence format.
- [ ] Document reports persistence format.
- [ ] Document per-user report grouping format.
- [ ] Build importer for existing V2 server definitions.
- [ ] Build importer for existing V2 reports/global ban data.

## SCP:5K config parity

- [ ] Management settings roundtrip.
- [ ] Map settings roundtrip.
- [ ] Players/admins/owners/whitelist roundtrip.
- [ ] Server settings roundtrip.
- [ ] Gameplay settings roundtrip.
- [ ] New map rotation format support.
- [ ] Shared install directory behavior.

## Server lifecycle parity

- [ ] Create server.
- [ ] Initialize server folder/config.
- [ ] Start server.
- [ ] Restart server.
- [ ] Stop server.
- [ ] Kill server.
- [ ] Stop after game behavior.
- [ ] Crash/restart behavior.
- [ ] Active-hours behavior.
- [ ] Empty-server behavior.

## Logs, chat, reports, bans

- [ ] Server log tailing.
- [ ] All-server log stream.
- [ ] Chat log parsing and paging.
- [ ] Report scanner.
- [ ] New-report count stream.
- [ ] Mark report handled.
- [ ] Delete report.
- [ ] Ban from report.
- [ ] Global banlist materialization.

## Web/UI parity

- [ ] Login.
- [ ] First-user creation.
- [ ] Logout.
- [ ] Dashboard server state.
- [ ] Server detail page.
- [ ] Create-server page.
- [ ] Logs page.
- [ ] Chat page.
- [ ] Reports page.
- [ ] Settings edit flows.

## V3 expansion gates

A3AINPC integration should not be treated as blocking V3 MVP until the above SCP:5K parity checklist has passing fixtures/tests.
