# Startup, database upgrades and readiness

Database migration errors stop startup before the API starts listening. Schema changes may be partially applied by databases whose DDL auto-commits; the version marker is not advanced until schema checks, legacy data changes and initial bootstrap all succeed. Bootstrap and the marker share a transaction, so a failed first boot can be retried. Startup rechecks schema at the current version to repair tables missing after an earlier interrupted upgrade. A database from a newer schema version is rejected. Back up data and upgrade all API instances together before deployment.

Login and administrative identity operations share the coordination introduced in schema 266. This batch adds no further schema version. Runtime database tests use isolated SQLite databases, including case-insensitive columns. MySQL/PostgreSQL use the GORM transaction/locking path; verify these backends before deployment.

`GET /api/health` returns HTTP 200 with `{"status":"ready"}` when startup completed and the database responds to a bounded ping. It returns 503 without database error details when the database is unavailable. Health is registered independently of business authentication and IP-ban middleware. HTTP bind failures also propagate to the process exit status.

Forwarded client IP headers are ignored by default. Configure `gin.trust-proxy` or `RUSTDESK_API_GIN_TRUST_PROXY` with a comma-separated list of actual proxy IPs/CIDRs. Empty lists mean no trusted proxy; invalid or blank entries in a nonempty list stop startup. For hbbs proxying in the same container, trust `127.0.0.1,::1`. Add your reverse proxy address explicitly when it connects through another address. The ingress proxy must overwrite client-supplied forwarding headers.

The combined server/API source image probes `/api/health`; older supplied API images returning 404 fall back to `/api/version`. A 503 from the new health endpoint never falls back to a weaker check. The combined image defaults proxy trust to loopback. Override it explicitly if your deployment has additional trusted proxy hops. Standalone API deployments have no implicit trust default.

No requests are automatically replayed by this readiness mechanism. Connection failures to the API origin are reported by hbbs as 503; timeout and malformed-response failures retain their separate status classes. Request deduplication, structured API security events and historical inventory merges are separate work.
