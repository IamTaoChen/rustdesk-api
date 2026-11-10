# Device identity and ID changes

`POST /api/sysinfo` associates a device using its nonempty UUID. The UUID is an opaque client value, including the base64 representation used by official clients; it need not be an RFC4122 string. IDs are limited to 253 bytes, UUIDs to 1024 bytes. Empty values, surrounding whitespace, invalid UTF-8 and control characters are rejected.

When the UUID identifies one record and the requested ID is available, the existing row is updated. Its row ID, creation time, group, alias, user association and last-online information are preserved. Machine information is refreshed, including intentionally empty values. UUID matching, login association and token migration use byte-exact identity checks even with case-insensitive database collations. Existing login-based user association remains available for unassigned devices. Active token records matching both UUID and old device ID follow the rename. Historical login/audit records and ID-only address-book entries are not rewritten.

An exact-ID legacy row with an empty UUID can be associated with a reported UUID only when that UUID has no other row. An existing ID with a different UUID, repeated UUID/ID records, or distinct rows matching the submitted ID and UUID returns HTTP 409 with `device_identity_conflict`. No record is silently merged or deleted. Invalid identity returns HTTP 400; database failures return HTTP 500 without database details. Successful sysinfo retains the plain `SYSINFO_UPDATED` response expected by clients.

`POST /api/heartbeat` requires both current ID and UUID. A missing or stale association returns HTTP 200 with `{"sysinfo":true}` to trigger a new system-information upload. An exact association returns `{}` and updates online time/IP at most once per 30 seconds. Heartbeats do not rename devices or change UUIDs. Validation and update share a transaction with a row lock so a concurrent case-only rename cannot accept a stale identity. SQLite does not support these row locks, so heartbeats acquire the identity coordination write lock before reading the device. This avoids read-to-write lock upgrade failures under concurrent heartbeats, reports or administrative writes in both rollback-journal and WAL modes. Lock waits that exceed the connection's busy timeout can still return a database failure.

Schema version 266 adds `peer_identity_locks`, a coordination table with one lazily created row. Report transactions lock this row before matching and writing devices, so simultaneous reports from multiple API instances do not independently create records. SQLite, MySQL and PostgreSQL use the same GORM transaction path. Upgrade all API instances before relying on this coordination; older binaries and direct administrative database writes do not participate. Existing devices are not modified by this schema migration. Back up the database before upgrading.

Existing duplicate records must be reviewed separately. Do not add UUID uniqueness constraints until duplicate and empty-UUID records have been reconciled. This change provides no automatic data cleanup.

System-information reports and heartbeats retain the existing anonymous client protocol. A UUID is an association key, not a password, signature or proof of device ownership. This change does not authenticate devices or modify user login policy. Restrict the API ingress to the deployment's intended clients/trusted path; knowing a UUID is not cryptographic authorization to rename a device. Keep such endpoints out of deployments that require authenticated device inventory until that authentication is implemented.

Validation uses isolated databases and Docker. No running database, production configuration or live device data is used by the tests.

## Administrative edits and sessions

Administrative creation and editing, report reconciliation, device login binding, logout and peer deletion now acquire the same identity coordination lock. Administrative creation rejects an occupied ID or UUID. Editing preserves the assigned UUID (an omitted UUID keeps its value); changing a nonempty UUID requires a separate identity-reset workflow. Group removal and empty aliases remain supported. An inventory ID edit updates matching active token metadata but does not rename the device in hbbs or on the client.

Login commits the token, login log and exact device binding together, and the login log references the token row ID. Web/admin login without UUID and legacy ID-only login remain compatible. A device login can precede its initial system-information report; a conflicting or stale existing device identity fails without leaving token/log rows. Logout revokes the exact token and clears only the same user's byte-exact UUID associations, including any historical duplicate inventory rows. Deleting a legacy empty-UUID device never revokes browser tokens. Peer deletion and token revocation commit together.

When a report has no current owner, automatic association requires a login log linked to an existing, unexpired token with the exact same UUID, device ID and user. Logout or device deletion revokes that association; historical login logs alone cannot restore it. Older logs with incorrect token references do not establish a new association; log in again. Existing stored ownership is preserved during reports.

## SQLite transaction audit

SQLite read transactions can fail immediately when upgrading to writes if another writer has intervened. Increasing the busy timeout or enabling WAL alone does not remove that failure mode. Request paths were checked for reads before the first transactional write:

| Request path | Transaction behavior |
| --- | --- |
| Heartbeat | Acquires the SQLite write lock before checking the exact device identity. |
| Sysinfo, device login/logout, administrative device creation/edit/deletion and UUID binding | Already acquire the identity coordination lock before transactional reads. Browser login starts with a token insert. |
| Legacy `POST /api/ab` device and tag reconciliation | Both now acquire the SQLite write lock before reading. Peer metadata lookups use the address-book transaction's connection. Query, write and commit errors are returned, and failed transactions roll back. |
| User deletion, OAuth registration and address-book collection deletion | The first database statement on the transaction is a write; there is no read-to-write upgrade. OAuth username lookups use separate statements outside its transaction. |
| Client user, peer and accessible device-group lists | The handlers are read-only. Shared authentication middleware may renew the token using a separate, single-write transaction; it does not upgrade the preceding lookup transaction. |

The SQLite writer helper reuses `peer_identity_locks`; no schema change is required. MySQL and PostgreSQL keep their existing transaction behavior. Address-book and tag reconciliation retain separate transactions: if the tag phase fails, the address-book phase may already have committed. `POST /api/ab` now reports database failures as HTTP 500 with `address_book_update_failed`, without exposing database details.

SQLite still permits only one writer at a time. Long-held locks or an exhausted busy timeout can therefore affect other reads and writes; this fix addresses the confirmed read-to-write upgrade failures. Regression tests use separate connections and temporary databases in both DELETE and WAL modes.
