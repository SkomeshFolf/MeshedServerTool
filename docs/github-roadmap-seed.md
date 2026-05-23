# GitHub roadmap seed

Use these epics/issues to keep the V3 design process Meshed-first.

## Epics

- EPIC: MeshedServerTool V2 behavior inventory
- EPIC: V3 core control plane
- EPIC: SCP:5K parity
- EPIC: V3 test harness
- EPIC: A3AINPC managed module integration
- EPIC: Arma text-only AI MVP
- EPIC: Private voiced Arma
- EPIC: Hardening and distribution

## First 20 issues

1. Inventory current MeshedServerTool V2 routes and features
2. Document V2 storage layout and platformdirs paths
3. Document V2 server lifecycle behavior
4. Document V2 shared install semantics
5. Build SCP:5K config fixtures
6. Build SCP:5K sample log fixtures
7. Build SCP:5K report fixtures
8. Scaffold V3 monorepo
9. Add Prisma ManagedServer/ManagedService schema
10. Add GameProfile registry
11. Add ManagedServiceProfile registry
12. Add ProcessManager with dummy process tests
13. Add ConfigMaterializer service
14. Add LogTailer with offset/rotation tests
15. Add EventBus and RuntimeEvent persistence
16. Add auth/session baseline
17. Add REST server-control API
18. Add Socket.IO state-stream API
19. Port SCP:5K legacy importer
20. Port SCP:5K start/restart/stop/kill lifecycle

A3AINPC issues should come after this foundation unless they are pure research gates.
