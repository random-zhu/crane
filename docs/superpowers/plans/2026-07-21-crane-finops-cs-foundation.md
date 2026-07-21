# Crane FinOps C/S Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the first production-capable C/S foundation in which each cluster's `craned` registers and maintains an outbound mTLS gRPC stream to a highly available central `crane-server`, with tenant-safe MySQL persistence, reliable ACK/resume semantics, basic inventory reporting, certificate rotation, and Desired State synchronization.

**Architecture:** Add one central modular `crane-server` binary and embed a leader-elected FinOps client in the existing `craned`; do not deploy OpenCost or any new persistent service in managed clusters. The center owns registration, PKI, sessions, batch persistence, ACK high-water marks, and Desired State, while each cluster keeps its private key in a Kubernetes Secret and remains locally autonomous. Phase 1 defines typed Inventory and Usage protocol messages but only emits basic Node/Namespace inventory; OpenCost allocation, cloud bills, UsageWindow collection, optimization execution, and Dashboard work belong to later plans.

**Tech Stack:** Go 1.25, gRPC 1.72.2, Protobuf 1.36.11, Buf, controller-runtime 0.23.3, Kubernetes 1.35 client libraries, MySQL 8, `database/sql`, `github.com/go-sql-driver/mysql`, standard-library `crypto/x509`, Prometheus client, Cobra, Kind, Docker, Kustomize.

## Global Constraints

- Follow `docs/superpowers/specs/2026-07-21-crane-opencost-cs-finops-platform-design.md`.
- Managed clusters keep the existing `crane-agent` DaemonSet and `craned` Deployment; do not add an OpenCost process, queue, database, or cost service.
- In protocol code, “Agent” means the cluster connection role implemented inside `craned`, never the node-level `crane-agent` DaemonSet.
- All cluster connections are outbound from `craned`; `crane-server` never stores kubeconfig and never calls a managed Kubernetes API.
- gRPC uses TLS on every connection. `RegisterCluster` is the only RPC allowed without a client certificate, and it requires a one-time bearer token.
- Client private keys are generated and retained in the managed cluster. The center signs CSRs and never receives private-key bytes.
- Every tenant-owned Store/Repository operation receives an explicit tenant identity, and every
  tenant-owned SQL statement includes `tenant_id` in its predicate or key.
- ACK is returned only after the corresponding MySQL transaction commits.
- Ingestion is at-least-once and idempotent by `(tenant_id, cluster_id, epoch, sequence)`.
- Protocol and Store amounts are strings when decimal resource quantities are needed; do not use `float64` for quantities that later feed financial calculations.
- Phase 1 does not execute Recommendation, EHPA, EVPA, Kubernetes patches, or arbitrary Desired State payloads.
- Phase 1 does not add Kafka, ClickHouse, Thanos, an OpenCost dependency, or S3.
- New functionality is guarded by `FinOpsAgent=false` by default in `craned`.
- Preserve all existing `craned`, Recommendation, EHPA, Dashboard, and cost-collector behavior when the feature gate is disabled.
- Generated Protobuf files are committed, reproducible with `make finops-proto`, and checked in CI.
- Production `crane-server` requires MySQL 8 and at least two replicas; tests may use one replica except the HA/E2E test.
- Containers run as non-root with a read-only root filesystem and the Restricted Pod Security profile.

---

## File Structure

Create or modify the following ownership boundaries. Later tasks consume only the interfaces explicitly listed in their `Interfaces` blocks.

```text
buf.yaml                                      repository-local Protobuf module
buf.gen.yaml                                  deterministic Go/gRPC generation
pkg/finops/proto/v1/finops.proto              versioned C/S wire contract
pkg/finops/proto/v1/*.pb.go                   generated messages and stubs
pkg/finops/proto/v1/checksum.go               canonical batch checksum helpers

pkg/finops/model/types.go                     tenant, cluster, batch, session, Desired State types
pkg/finops/store/store.go                     persistence interfaces and typed errors
pkg/finops/store/contract/contract.go          reusable persistence conformance suite
pkg/finops/store/memory/store.go               deterministic unit-test backend
pkg/finops/store/mysql/migrate.go              embedded forward-only migrations
pkg/finops/store/mysql/migrations/*.sql        MySQL 8 schema
pkg/finops/store/mysql/store.go                registry/session/batch/Desired State repository

pkg/finops/pki/issuer.go                       certificate issuer contract
pkg/finops/pki/x509.go                         CSR validation and X.509 signing
pkg/finops/bootstrap/service.go                tenant and one-time token lifecycle
pkg/finops/gateway/auth.go                     bearer and peer-certificate authentication
pkg/finops/gateway/server.go                   Register/Rotate/Connect gRPC implementation
pkg/finops/gateway/stream.go                   handshake, ingest, ACK, Desired State loop
pkg/finops/gateway/metrics.go                  bounded-cardinality gateway metrics

cmd/crane-server/main.go                       central binary entry point
cmd/crane-server/app/options.go                validated central configuration
cmd/crane-server/app/command.go                serve and admin Cobra commands
cmd/crane-server/app/server.go                 HTTP/gRPC/DB lifecycle

pkg/clusteragent/identity/store.go              managed-cluster identity contract
pkg/clusteragent/identity/kubernetes.go         Secret-backed key/certificate storage
pkg/clusteragent/client/client.go               registration, rotation, and mTLS dialing
pkg/clusteragent/reporter/outbox.go             bounded ordered in-memory outbox
pkg/clusteragent/reporter/inventory.go          Phase 1 Node/Namespace inventory source
pkg/clusteragent/reporter/reporter.go           reconnect, resume, heartbeat, and Desired ACK
pkg/clusteragent/reporter/metrics.go            connection, identity, ACK, and outbox metrics
cmd/craned/app/options/finops.go                FinOps Agent flags and validation

deploy/crane-server/                            central production manifests
deploy/craned/                                  identity RBAC and disabled-by-default integration
hack/finops/                                    MySQL and three-Kind-cluster E2E harness
docs/operations/finops-cs-foundation.md         bootstrap, rotation, recovery, and rollback runbook
```

## Phase 1 Coverage Map

| Approved Phase 1 requirement | Implemented and verified by |
|---|---|
| Versioned outbound C/S protocol | Tasks 1, 5, 8, 11 |
| One-time bootstrap, mTLS, certificate rotation/recovery | Tasks 4, 5, 7, 11, 12 |
| Tenant-safe MySQL registry, sessions, batches, Desired State | Tasks 2, 3, 5 |
| Commit-before-ACK, duplicate handling, contiguous resume | Tasks 2, 3, 5, 8, 11 |
| One leader-elected Reporter inside existing `craned` | Tasks 8 and 9 |
| Basic Node/Namespace inventory without raw time-series upload | Tasks 1, 8, 11 |
| Center HA and same-epoch reconnect fencing | Tasks 3, 5, 11 |
| Disabled-by-default compatibility and local autonomy | Tasks 9, 11, 12 |
| Two-replica Restricted deployment with external MySQL | Tasks 6 and 10 |
| 50-cluster connection capacity | Tasks 11 and 12 |
| Operations, upgrade, revocation, and rollback | Task 12 |

---

### Task 1: Define and Generate the Versioned FinOps Protocol

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Modify: `Makefile`
- Create: `buf.yaml`
- Create: `buf.gen.yaml`
- Create: `pkg/finops/proto/v1/finops.proto`
- Create: `pkg/finops/proto/v1/finops.pb.go` (generated)
- Create: `pkg/finops/proto/v1/finops_grpc.pb.go` (generated)
- Create: `pkg/finops/proto/v1/checksum.go`
- Create: `pkg/finops/proto/v1/protocol_test.go`

**Interfaces:**
- Consumes: existing `google.golang.org/grpc` and `google.golang.org/protobuf` dependencies.
- Produces: `finopsv1.FinOpsGatewayClient`, `finopsv1.FinOpsGatewayServer`, typed registration/rotation RPCs, bidirectional `Connect`, `AgentMessage`, `ServerMessage`, `InventoryDelta`, `UsageWindow`, and `DesiredStateSnapshot`.

- [ ] **Step 1: Add the failing wire-contract test**

Create `pkg/finops/proto/v1/protocol_test.go`:

```go
package finopsv1_test

import (
    "testing"
    "time"

    finopsv1 "github.com/gocrane/crane/pkg/finops/proto/v1"
    "google.golang.org/protobuf/proto"
    "google.golang.org/protobuf/types/known/timestamppb"
)

func TestAgentMessageRoundTripPreservesIdentityAndSequence(t *testing.T) {
    original := &finopsv1.AgentMessage{
        TenantId: "tenant-a", ClusterId: "cluster-a", Epoch: 7,
        Body: &finopsv1.AgentMessage_InventoryDelta{InventoryDelta: &finopsv1.InventoryDelta{
            Header: &finopsv1.BatchHeader{
                Sequence: 9, SchemaVersion: 1,
                WindowStart: timestamppb.New(time.Unix(100, 0)),
                WindowEnd: timestamppb.New(time.Unix(200, 0)),
                Checksum: []byte("01234567890123456789012345678901"),
            },
            Resources: []*finopsv1.InventoryResource{{
                Uid: "uid-1", ApiVersion: "v1", Kind: "Namespace", Name: "default",
                ResourceVersion: "12", Labels: map[string]string{"cost-center": "platform"},
            }},
        }},
    }
    encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(original)
    if err != nil { t.Fatal(err) }
    decoded := &finopsv1.AgentMessage{}
    if err := proto.Unmarshal(encoded, decoded); err != nil { t.Fatal(err) }
    if decoded.GetTenantId() != "tenant-a" || decoded.GetClusterId() != "cluster-a" || decoded.GetEpoch() != 7 {
        t.Fatalf("identity lost: %#v", decoded)
    }
    if got := decoded.GetInventoryDelta().GetHeader().GetSequence(); got != 9 {
        t.Fatalf("sequence = %d, want 9", got)
    }
}
```

- [ ] **Step 2: Run the test and confirm the missing package failure**

Run: `go test ./pkg/finops/proto/v1 -run TestAgentMessageRoundTripPreservesIdentityAndSequence -count=1`

Expected: FAIL because `pkg/finops/proto/v1` and its generated types do not exist.

- [ ] **Step 3: Add deterministic Protobuf tooling**

Add Go 1.25 tool dependencies pinned to these versions:

```go
tool (
    github.com/bufbuild/buf/cmd/buf
    google.golang.org/grpc/cmd/protoc-gen-go-grpc
    google.golang.org/protobuf/cmd/protoc-gen-go
)
```

Run:

```bash
go get -tool github.com/bufbuild/buf/cmd/buf@v1.47.2
go get -tool google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
go get -tool google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
```

Create `buf.yaml`:

```yaml
version: v2
modules:
  - path: pkg/finops/proto/v1
lint:
  use:
    - STANDARD
breaking:
  use:
    - FILE
```

Create `buf.gen.yaml`:

```yaml
version: v2
plugins:
  - local: [go, tool, protoc-gen-go]
    out: pkg/finops/proto/v1
    opt:
      - paths=source_relative
  - local: [go, tool, protoc-gen-go-grpc]
    out: pkg/finops/proto/v1
    opt:
      - paths=source_relative
      - require_unimplemented_servers=true
```

Add to `Makefile`:

```make
.PHONY: finops-proto
finops-proto: ## Generate the FinOps C/S protocol.
	go tool buf lint
	go tool buf generate --template buf.gen.yaml
```

- [ ] **Step 4: Define the complete Phase 1 wire contract**

Create `pkg/finops/proto/v1/finops.proto` with this public surface:

```proto
syntax = "proto3";

package finops.crane.io.v1;

option go_package = "github.com/gocrane/crane/pkg/finops/proto/v1;finopsv1";

import "google/protobuf/timestamp.proto";

service FinOpsGateway {
  rpc RegisterCluster(RegisterClusterRequest) returns (CertificateResponse);
  rpc RotateCertificate(RotateCertificateRequest) returns (CertificateResponse);
  rpc Connect(stream AgentMessage) returns (stream ServerMessage);
}

message RegisterClusterRequest {
  string tenant_id = 1;
  string cluster_id = 2;
  string cluster_uid = 3;
  bytes csr_der = 4;
  string agent_version = 5;
  string kubernetes_version = 6;
  repeated string capabilities = 7;
}

message RotateCertificateRequest { bytes csr_der = 1; }

message CertificateResponse {
  bytes certificate_pem = 1;
  bytes client_ca_pem = 2;
  google.protobuf.Timestamp expires_at = 3;
  string serial_number = 4;
  uint64 epoch_floor = 5;
}

message AgentMessage {
  string tenant_id = 1;
  string cluster_id = 2;
  uint64 epoch = 3;
  oneof body {
    ClusterHello hello = 10;
    InventoryDelta inventory_delta = 11;
    UsageWindow usage_window = 12;
    Heartbeat heartbeat = 13;
    DesiredStateAck desired_state_ack = 14;
    ExecutionStatus execution_status = 15;
  }
}

message ServerMessage {
  oneof body {
    ServerAck ack = 10;
    DesiredStateSnapshot desired_state = 11;
    RotateCertificateNotice rotate_certificate = 12;
    ProtocolError error = 13;
  }
}

message ClusterHello {
  string agent_version = 1;
  string kubernetes_version = 2;
  repeated string capabilities = 3;
  uint64 last_acked_sequence = 4;
  uint64 last_desired_state_revision = 5;
}

message BatchHeader {
  uint64 sequence = 1;
  uint32 schema_version = 2;
  google.protobuf.Timestamp window_start = 3;
  google.protobuf.Timestamp window_end = 4;
  bytes checksum = 5;
}

message InventoryDelta {
  BatchHeader header = 1;
  bool full_snapshot = 2;
  repeated InventoryResource resources = 3;
}

message InventoryResource {
  string uid = 1;
  string api_version = 2;
  string kind = 3;
  string namespace = 4;
  string name = 5;
  string resource_version = 6;
  bool deleted = 7;
  string owner_uid = 8;
  map<string, string> labels = 9;
  map<string, string> attributes = 10;
}

message UsageWindow {
  BatchHeader header = 1;
  repeated UsageRecord records = 2;
  string coverage = 3;
}

message UsageRecord {
  string resource_uid = 1;
  string node_uid = 2;
  string container = 3;
  string cpu_usage_core_seconds = 4;
  string cpu_request_core_seconds = 5;
  string memory_usage_byte_seconds = 6;
  string memory_request_byte_seconds = 7;
  string gpu_seconds = 8;
  string pv_provisioned_byte_seconds = 9;
  string pv_used_byte_seconds = 10;
  uint64 network_ingress_bytes = 11;
  uint64 network_egress_bytes = 12;
}

message Heartbeat {
  google.protobuf.Timestamp observed_at = 1;
  google.protobuf.Timestamp inventory_fresh_at = 2;
  google.protobuf.Timestamp usage_fresh_at = 3;
  google.protobuf.Timestamp slo_fresh_at = 4;
  uint64 outbox_depth = 5;
  int64 clock_skew_seconds = 6;
}

message DesiredStateSnapshot {
  uint64 revision = 1;
  repeated PlanReference plans = 2;
  bytes checksum = 3;
  google.protobuf.Timestamp created_at = 4;
}

message PlanReference {
  string plan_id = 1;
  uint64 revision = 2;
  bytes digest = 3;
  google.protobuf.Timestamp expires_at = 4;
}

message DesiredStateAck {
  uint64 revision = 1;
  bytes checksum = 2;
}

message ExecutionStatus {
  string plan_id = 1;
  uint64 revision = 2;
  string state = 3;
  string reason_code = 4;
  google.protobuf.Timestamp observed_at = 5;
}

message ServerAck {
  uint64 epoch = 1;
  uint64 sequence = 2;
  bool duplicate = 3;
}

message RotateCertificateNotice { google.protobuf.Timestamp rotate_before = 1; }

message ProtocolError {
  string code = 1;
  string message = 2;
  bool reconnect = 3;
}
```

The bearer bootstrap token is sent only in gRPC metadata as `authorization: Bearer <token>`; it is not a Protobuf field.

- [ ] **Step 5: Implement canonical batch checksums**

Add helpers with these exact signatures:

```go
func SetInventoryChecksum(delta *InventoryDelta) error
func VerifyInventoryChecksum(delta *InventoryDelta) error
func SetUsageChecksum(window *UsageWindow) error
func VerifyUsageChecksum(window *UsageWindow) error
```

Each helper clones the message with `proto.Clone`, requires a non-nil header, clears
`header.checksum`, marshals with `proto.MarshalOptions{Deterministic: true}`, and computes
SHA-256 over those bytes. `Set` writes the 32-byte digest to the original message. `Verify`
requires an existing 32-byte digest and uses `subtle.ConstantTimeCompare`.

Extend `protocol_test.go` to call `SetInventoryChecksum`, require `VerifyInventoryChecksum` to
pass, mutate one resource name, and require verification to fail. Repeat for one UsageRecord so
both helpers are covered.

- [ ] **Step 6: Generate, lint, and run protocol tests**

Run:

```bash
make finops-proto
go test ./pkg/finops/proto/v1 -count=1
git diff --exit-code -- pkg/finops/proto/v1/finops.pb.go pkg/finops/proto/v1/finops_grpc.pb.go
```

Expected: Buf lint PASS, protocol test PASS, and regeneration leaves no diff.

- [ ] **Step 7: Commit the protocol**

```bash
git add go.mod go.sum Makefile buf.yaml buf.gen.yaml pkg/finops/proto/v1
git commit -m "feat: define finops cs protocol"
```

---

### Task 2: Define Tenant-Safe Domain and Store Contracts

**Files:**
- Create: `pkg/finops/model/types.go`
- Create: `pkg/finops/model/types_test.go`
- Create: `pkg/finops/store/store.go`
- Create: `pkg/finops/store/contract/contract.go`
- Create: `pkg/finops/store/memory/store.go`
- Create: `pkg/finops/store/memory/store_test.go`

**Interfaces:**
- Consumes: serialized typed messages from Task 1.
- Produces: `model.Identity`, `model.Cluster`, `model.BootstrapToken`, `model.Session`, `model.Batch`, `model.DesiredState`, `store.Store`, and a reusable `contract.Run` suite consumed by MySQL in Task 3.

- [ ] **Step 1: Write failing identity and repository contract tests**

Create validation tests that require DNS-label-safe tenant/cluster IDs and a contract that proves
tenant isolation, one-time token redemption/revocation, certificate rotation overlap,
same-epoch connection fencing, idempotent ingestion, ACK high-water marks, and Desired State ACK:

```go
func TestIdentityValidate(t *testing.T) {
    for _, tc := range []struct{ tenant, cluster string; valid bool }{
        {"tenant-a", "cluster-1", true},
        {"Tenant-A", "cluster-1", false},
        {"tenant-a", "../cluster", false},
        {"", "cluster-1", false},
    } {
        err := (model.Identity{TenantID: tc.tenant, ClusterID: tc.cluster}).Validate()
        if (err == nil) != tc.valid { t.Fatalf("%q/%q: %v", tc.tenant, tc.cluster, err) }
    }
}

func TestMemoryStoreContract(t *testing.T) {
    contract.Run(t, func(t *testing.T) store.Store {
        return memory.New()
    })
}
```

`contract.Run` must include this cross-tenant assertion:

```go
if _, err := subject.GetCluster(ctx, model.Identity{TenantID: "tenant-b", ClusterID: "cluster-a"});
    !errors.Is(err, store.ErrNotFound) {
    t.Fatalf("cross-tenant read = %v, want ErrNotFound", err)
}
```

- [ ] **Step 2: Run tests and confirm missing-type failures**

Run: `go test ./pkg/finops/model ./pkg/finops/store/... -count=1`

Expected: FAIL because domain and Store types do not exist.

- [ ] **Step 3: Add exact domain types and validation**

Implement these types in `pkg/finops/model/types.go`:

```go
type TenantID string
type ClusterID string
type Identity struct { TenantID TenantID; ClusterID ClusterID }
type Tenant struct { ID TenantID; CreatedAt time.Time }

type Cluster struct {
    Identity          Identity
    UID               string
    AgentVersion      string
    KubernetesVersion string
    Capabilities      []string
    CertificateSerial string
    CertificateExpiry time.Time
    LastSeenAt        time.Time
}

type BootstrapToken struct {
    Identity        Identity
    Hash            [32]byte
    ReplaceIdentity bool
    ExpiresAt       time.Time
    CreatedAt       time.Time
}

type Registration struct {
    Cluster   Cluster
    TokenHash [32]byte
}

type RegistrationResult struct {
    Cluster    Cluster
    EpochFloor uint64
}

type Session struct {
    Identity       Identity
    Epoch          uint64
    ConnectionID   string
    ServerInstance string
    ConnectedAt    time.Time
    DisconnectedAt time.Time
    LastAcked      uint64
    ReconnectCount uint64
}

type BatchKind string
const (
    BatchInventory BatchKind = "inventory"
    BatchUsage     BatchKind = "usage"
)

type Batch struct {
    Identity      Identity
    Epoch         uint64
    Sequence      uint64
    Kind          BatchKind
    SchemaVersion uint32
    WindowStart   time.Time
    WindowEnd     time.Time
    Checksum      [32]byte
    Payload       []byte
    CreatedAt     time.Time
}

type IngestResult struct { Duplicate bool; AckSequence uint64 }

type DesiredState struct {
    Identity  Identity
    Revision  uint64
    Checksum  [32]byte
    Payload   []byte
    CreatedAt time.Time
}
```

`Identity.Validate` accepts lowercase alphanumeric IDs with `-` or `.`, 1-63 characters, starting and ending alphanumeric. `Batch.Validate` requires nonzero epoch and sequence, valid kind, schema version `1`, exactly 32 checksum bytes represented by `[32]byte`, a non-empty deterministic Protobuf payload, and `WindowEnd.After(WindowStart)`.

- [ ] **Step 4: Define Store interfaces and errors**

Implement in `pkg/finops/store/store.go`:

```go
var (
    ErrNotFound      = errors.New("finops object not found")
    ErrAlreadyExists = errors.New("finops object already exists")
    ErrTokenExpired  = errors.New("bootstrap token expired")
    ErrTokenUsed     = errors.New("bootstrap token already used")
    ErrConflict      = errors.New("finops state conflict")
    ErrStaleEpoch    = errors.New("stale agent epoch")
)

type Store interface {
    CreateTenant(context.Context, model.Tenant) error
    CreateBootstrapToken(context.Context, model.BootstrapToken) error
    RedeemBootstrapToken(context.Context, model.Registration, time.Time) (model.RegistrationResult, error)
    RotateCertificate(context.Context, model.Identity, string, string, time.Time, time.Time) error
    AuthorizeCertificate(context.Context, model.Identity, string, time.Time) error
    RevokeClusterIdentity(context.Context, model.Identity, time.Time) error
    GetCluster(context.Context, model.Identity) (model.Cluster, error)
    ListClusters(context.Context, model.TenantID) ([]model.Cluster, error)
    OpenSession(context.Context, model.Session) error
    AuthorizeSession(context.Context, model.Identity, uint64, string) error
    CloseSession(context.Context, model.Identity, uint64, string, time.Time) error
    RecordHeartbeat(context.Context, model.Identity, uint64, string, time.Time) error
    StoreBatch(context.Context, string, model.Batch) (model.IngestResult, error)
    LastAck(context.Context, model.Identity, uint64) (uint64, error)
    PutDesiredState(context.Context, model.DesiredState) error
    GetDesiredState(context.Context, model.Identity) (model.DesiredState, error)
    AckDesiredState(context.Context, model.Identity, uint64, [32]byte, time.Time) error
    Close() error
}
```

Every tenant-owned method validates identity before accessing data. `ListClusters` requires a
validated `model.TenantID` and never lists across tenants.

The two serial arguments to `RotateCertificate` are previous and newly issued serials; the two
time arguments are new certificate expiry and old-certificate overlap expiry. The bootstrap
service always uses a 10-minute overlap. `AuthorizeCertificate` accepts the current certificate
or a non-revoked previous certificate still inside that overlap window.

- [ ] **Step 5: Implement memory Store and complete conformance coverage**

The memory implementation must lock all state, clone slices/bytes on both writes and reads,
atomically consume tokens, reject an older session epoch, fence a replaced connection ID within
the same epoch, reject an epoch gap larger than one, accept an identical repeated batch as
`Duplicate=true`, reject the same key with
a changed checksum as `ErrConflict`, and advance ACK only across contiguous sequences. If
sequence 3 arrives before sequence 2, ACK stays at 1 until sequence 2 commits, then advances to 3.

Run: `go test -race ./pkg/finops/model ./pkg/finops/store/... -count=1`

Expected: PASS with no races.

- [ ] **Step 6: Commit domain contracts**

```bash
git add pkg/finops/model pkg/finops/store
git commit -m "feat: define finops tenant store contracts"
```

---

### Task 3: Implement MySQL 8 Migrations and Store

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Create: `pkg/finops/store/mysql/migrate.go`
- Create: `pkg/finops/store/mysql/migrate_test.go`
- Create: `pkg/finops/store/mysql/migrations/0001_cs_foundation.sql`
- Create: `pkg/finops/store/mysql/store.go`
- Create: `pkg/finops/store/mysql/store_test.go`
- Create: `hack/finops/docker-compose.yaml`

**Interfaces:**
- Consumes: `store.Store` and contract suite from Task 2.
- Produces: `mysqlstore.Open(ctx context.Context, dsn string) (*Store, error)` and `mysqlstore.Migrate(ctx context.Context, db *sql.DB) error`.

- [ ] **Step 1: Add the MySQL driver and start the test database**

Run:

```bash
go get github.com/go-sql-driver/mysql@v1.9.3
docker compose -f hack/finops/docker-compose.yaml up -d mysql
```

Create a compose service using `mysql:8.4`, database `crane_finops`, user/password `crane/crane`, root password `root`, healthcheck `mysqladmin ping`, and host port `33306`.

- [ ] **Step 2: Write failing migration and Store contract tests**

```go
func TestMigrateIsIdempotent(t *testing.T) {
    db := openTestDB(t)
    require.NoError(t, resetDatabase(db))
    require.NoError(t, Migrate(context.Background(), db))
    require.NoError(t, Migrate(context.Background(), db))
    for _, table := range []string{"tenants", "clusters", "cluster_certificates", "bootstrap_tokens", "agent_sessions", "ingestion_batches", "desired_states"} {
        requireTable(t, db, table)
    }
}

func TestStoreContract(t *testing.T) {
    contract.Run(t, func(t *testing.T) store.Store {
        db := openIsolatedTestDB(t)
        require.NoError(t, Migrate(context.Background(), db))
        return New(db)
    })
}
```

Run: `CRANE_FINOPS_MYSQL_DSN='crane:crane@tcp(127.0.0.1:33306)/crane_finops?parseTime=true&multiStatements=true' go test ./pkg/finops/store/mysql -count=1`

Expected: FAIL because migrations and Store are not implemented.

- [ ] **Step 3: Add the forward-only schema**

Create `0001_cs_foundation.sql` with `utf8mb4_bin`, UTC `DATETIME(6)`, no database-level cascade across tenants, and these keys:

```sql
CREATE TABLE IF NOT EXISTS tenants (
  tenant_id VARCHAR(63) COLLATE utf8mb4_bin PRIMARY KEY,
  created_at DATETIME(6) NOT NULL
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS clusters (
  tenant_id VARCHAR(63) COLLATE utf8mb4_bin NOT NULL,
  cluster_id VARCHAR(63) COLLATE utf8mb4_bin NOT NULL,
  cluster_uid VARCHAR(128) NOT NULL,
  agent_version VARCHAR(64) NOT NULL,
  kubernetes_version VARCHAR(64) NOT NULL,
  capabilities JSON NOT NULL,
  certificate_serial VARCHAR(128) NOT NULL,
  certificate_expires_at DATETIME(6) NOT NULL,
  last_seen_at DATETIME(6) NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (tenant_id, cluster_id),
  UNIQUE KEY uq_cluster_uid (tenant_id, cluster_uid),
  CONSTRAINT fk_clusters_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(tenant_id)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS bootstrap_tokens (
  token_hash BINARY(32) PRIMARY KEY,
  tenant_id VARCHAR(63) COLLATE utf8mb4_bin NOT NULL,
  cluster_id VARCHAR(63) COLLATE utf8mb4_bin NOT NULL,
  replace_identity BOOLEAN NOT NULL DEFAULT FALSE,
  expires_at DATETIME(6) NOT NULL,
  used_at DATETIME(6) NULL,
  revoked_at DATETIME(6) NULL,
  created_at DATETIME(6) NOT NULL,
  KEY idx_bootstrap_scope (tenant_id, cluster_id),
  CONSTRAINT fk_bootstrap_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(tenant_id)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS cluster_certificates (
  tenant_id VARCHAR(63) COLLATE utf8mb4_bin NOT NULL,
  cluster_id VARCHAR(63) COLLATE utf8mb4_bin NOT NULL,
  serial_number VARCHAR(128) NOT NULL,
  expires_at DATETIME(6) NOT NULL,
  overlap_expires_at DATETIME(6) NULL,
  revoked_at DATETIME(6) NULL,
  issued_at DATETIME(6) NOT NULL,
  PRIMARY KEY (tenant_id, cluster_id, serial_number),
  CONSTRAINT fk_certificates_cluster FOREIGN KEY (tenant_id, cluster_id)
    REFERENCES clusters(tenant_id, cluster_id)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS agent_sessions (
  tenant_id VARCHAR(63) COLLATE utf8mb4_bin NOT NULL,
  cluster_id VARCHAR(63) COLLATE utf8mb4_bin NOT NULL,
  epoch BIGINT UNSIGNED NOT NULL,
  connection_id VARCHAR(64) NOT NULL,
  server_instance VARCHAR(128) NOT NULL,
  connected_at DATETIME(6) NOT NULL,
  disconnected_at DATETIME(6) NULL,
  last_acked_sequence BIGINT UNSIGNED NOT NULL DEFAULT 0,
  last_heartbeat_at DATETIME(6) NULL,
  reconnect_count BIGINT UNSIGNED NOT NULL DEFAULT 0,
  PRIMARY KEY (tenant_id, cluster_id, epoch),
  CONSTRAINT fk_sessions_cluster FOREIGN KEY (tenant_id, cluster_id)
    REFERENCES clusters(tenant_id, cluster_id)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS ingestion_batches (
  tenant_id VARCHAR(63) COLLATE utf8mb4_bin NOT NULL,
  cluster_id VARCHAR(63) COLLATE utf8mb4_bin NOT NULL,
  epoch BIGINT UNSIGNED NOT NULL,
  sequence BIGINT UNSIGNED NOT NULL,
  kind ENUM('inventory','usage') NOT NULL,
  schema_version INT UNSIGNED NOT NULL,
  window_start DATETIME(6) NOT NULL,
  window_end DATETIME(6) NOT NULL,
  checksum BINARY(32) NOT NULL,
  payload LONGBLOB NOT NULL,
  created_at DATETIME(6) NOT NULL,
  PRIMARY KEY (tenant_id, cluster_id, epoch, sequence),
  KEY idx_batches_window (tenant_id, cluster_id, kind, window_start),
  CONSTRAINT fk_batches_session FOREIGN KEY (tenant_id, cluster_id, epoch)
    REFERENCES agent_sessions(tenant_id, cluster_id, epoch)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS desired_states (
  tenant_id VARCHAR(63) COLLATE utf8mb4_bin NOT NULL,
  cluster_id VARCHAR(63) COLLATE utf8mb4_bin NOT NULL,
  revision BIGINT UNSIGNED NOT NULL,
  checksum BINARY(32) NOT NULL,
  payload LONGBLOB NOT NULL,
  created_at DATETIME(6) NOT NULL,
  acked_at DATETIME(6) NULL,
  PRIMARY KEY (tenant_id, cluster_id, revision),
  CONSTRAINT fk_desired_cluster FOREIGN KEY (tenant_id, cluster_id)
    REFERENCES clusters(tenant_id, cluster_id)
) ENGINE=InnoDB;
```

Add a `schema_migrations(version BIGINT PRIMARY KEY, checksum BINARY(32), applied_at DATETIME(6))` table from the migration runner before applying embedded files.

- [ ] **Step 4: Implement checksum-locked migrations**

Use `//go:embed migrations/*.sql`. `Migrate` creates
`schema_migrations` with `CREATE TABLE IF NOT EXISTS`, acquires
`GET_LOCK('crane_finops_migrate', 30)`, sorts migration names, hashes exact bytes, rejects a
changed checksum for an applied version, executes the idempotent DDL file, records its checksum
only after the entire file succeeds, and releases the lock in `defer`. MySQL DDL implicitly
commits, so the implementation must not claim transactional rollback; a partial first migration
is recovered by rerunning its `IF NOT EXISTS` statements before writing the migration record.
Never edit an applied migration; add a new numbered file.

- [ ] **Step 5: Implement Store transactions with tenant predicates**

`CreateBootstrapToken` revokes every prior unused token for the same tenant/cluster before insert
and persists whether this is an administrator-authorized identity replacement.
`RedeemBootstrapToken` must lock the token row `FOR UPDATE`, compare tenant/cluster, reject used,
revoked, or expired tokens, and set `used_at` in one transaction. A normal token inserts a new
cluster plus its first `cluster_certificates` row and rejects an existing cluster. A replacement
token requires an existing cluster, revokes all prior certificates, fences active sessions by
changing their connection IDs and setting `disconnected_at`, updates the current certificate, and
returns the maximum stored session epoch without deleting inventory, batches, Desired State, or
history. `RotateCertificate` inserts the new serial and sets the old serial's
`overlap_expires_at`; `AuthorizeCertificate` rejects expired, revoked, or elapsed-overlap serials.
`RevokeClusterIdentity` revokes every certificate and fences active sessions without deleting the
cluster or historical data.

`OpenSession` accepts an initial epoch of 1 and thereafter only the current epoch (transport
reconnect) or current epoch plus one (new Leader/process); gaps and lower epochs return
`ErrStaleEpoch`. For the same epoch it atomically replaces `connection_id`
and `server_instance`, clears `disconnected_at`, and increments `reconnect_count`; every heartbeat,
close, and batch write must match the current connection ID so an old stream is fenced.
`AuthorizeSession` performs the same identity/epoch/connection check without mutating heartbeat;
the gateway calls it before every Desired State or rotation-notice send.
`StoreBatch` locks the matching session, rejects a stale epoch or connection ID, inserts with
`INSERT ... ON DUPLICATE KEY UPDATE sequence=sequence`, reads and compares checksum on
duplicates, then advances `last_acked_sequence` only through a contiguous `sequence + 1` scan in
the same transaction.

Every read/update query includes both `tenant_id = ?` and `cluster_id = ?`. Tests must inspect MySQL general behavior through cross-tenant fixtures, not string-match SQL.

- [ ] **Step 6: Run migration, contract, and race tests**

Run:

```bash
CRANE_FINOPS_MYSQL_DSN='crane:crane@tcp(127.0.0.1:33306)/crane_finops?parseTime=true&multiStatements=true' go test -race ./pkg/finops/store/mysql -count=1
```

Expected: PASS, including two consecutive migration runs and Store conformance.

- [ ] **Step 7: Commit MySQL persistence**

```bash
git add go.mod go.sum pkg/finops/store/mysql hack/finops/docker-compose.yaml
git commit -m "feat: persist finops cs state in mysql"
```

---

### Task 4: Implement CSR Signing and One-Time Bootstrap

**Files:**
- Create: `pkg/finops/pki/issuer.go`
- Create: `pkg/finops/pki/x509.go`
- Create: `pkg/finops/pki/x509_test.go`
- Create: `pkg/finops/bootstrap/service.go`
- Create: `pkg/finops/bootstrap/service_test.go`

**Interfaces:**
- Consumes: `store.Store`, `model.Identity`, and DER CSRs.
- Produces: `pki.Issuer`, `pki.NewX509Issuer`, `bootstrap.Service.CreateToken`, `bootstrap.Service.Register`, and `bootstrap.Service.Rotate`.

- [ ] **Step 1: Write failing PKI and bootstrap tests**

Tests must prove that requested CSR SANs are ignored, the issued client certificate contains only
the expected Crane URI identity, a raw token is returned once while only its SHA-256 is stored,
token reuse fails, token scope cannot register another cluster, expired tokens fail, normal tokens
cannot replace an identity, replacement tokens require an existing cluster and return its epoch
floor, and rotation requires an existing cluster.

```go
func TestIssuerReplacesRequestedSANWithCraneIdentity(t *testing.T) {
    issuer := newTestIssuer(t)
    csrDER := csrWithDNSName(t, "attacker.example")
    issued, err := issuer.SignClient(context.Background(), model.Identity{
        TenantID: "tenant-a", ClusterID: "cluster-a",
    }, csrDER, time.Unix(1000, 0))
    require.NoError(t, err)
    cert := parseCertificate(t, issued.CertificatePEM)
    require.Empty(t, cert.DNSNames)
    require.Equal(t, "spiffe://crane/tenant/tenant-a/cluster/cluster-a", cert.URIs[0].String())
    require.Equal(t, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, cert.ExtKeyUsage)
}
```

- [ ] **Step 2: Run tests and confirm missing implementations**

Run: `go test ./pkg/finops/pki ./pkg/finops/bootstrap -count=1`

Expected: FAIL because issuer and bootstrap services do not exist.

- [ ] **Step 3: Define the issuer contract and X.509 implementation**

```go
type IssuedCertificate struct {
    CertificatePEM []byte
    ClientCAPEM    []byte
    SerialNumber   string
    ExpiresAt      time.Time
}

type Issuer interface {
    SignClient(context.Context, model.Identity, []byte, time.Time) (IssuedCertificate, error)
}

func NewX509Issuer(caCertPEM, caKeyPEM []byte, validity time.Duration) (*X509Issuer, error)
```

Accept ECDSA P-256/P-384 and RSA 2048+ public keys. Verify CSR signatures, discard requested extensions/SANs, generate a random 128-bit serial, use `NotBefore = now.Add(-5*time.Minute)`, `NotAfter = now.Add(validity)`, `KeyUsageDigitalSignature`, and `ExtKeyUsageClientAuth`. Add only URI `spiffe://crane/tenant/<tenant>/cluster/<cluster>`.

- [ ] **Step 4: Implement token creation, registration, and rotation**

```go
type Service struct { Store store.Store; Issuer pki.Issuer; Now func() time.Time }

type RegistrationRequest struct {
    Identity          model.Identity
    ClusterUID        string
    AgentVersion      string
    KubernetesVersion string
    Capabilities      []string
    CSRDER            []byte
}

type RegistrationResult struct {
    Certificate pki.IssuedCertificate
    EpochFloor  uint64
}

func (s *Service) CreateToken(ctx context.Context, id model.Identity, ttl time.Duration, replaceIdentity bool) (string, error)
func (s *Service) Register(ctx context.Context, rawToken string, req RegistrationRequest) (RegistrationResult, error)
func (s *Service) Rotate(ctx context.Context, id model.Identity, previousSerial string, csrDER []byte) (pki.IssuedCertificate, error)
```

`CreateToken` reads 32 random bytes, returns base64url without padding, revokes prior unused
tokens for the same scope, and stores only SHA-256 plus `replaceIdentity`. TTL must be between
1 minute and 24 hours.
`Register` signs first, then atomically redeems the token with the issued serial/expiry; a
certificate whose DB transaction fails cannot connect because gateway authentication also checks
the registered serial. `Rotate` signs a new CSR and records a 10-minute overlap for the previous
serial, allowing the Agent to retry safely if the response is lost. Do not log or wrap the raw token.

- [ ] **Step 5: Run tests with race detection**

Run: `go test -race ./pkg/finops/pki ./pkg/finops/bootstrap -count=1`

Expected: PASS.

- [ ] **Step 6: Commit PKI and bootstrap**

```bash
git add pkg/finops/pki pkg/finops/bootstrap
git commit -m "feat: bootstrap finops cluster identities"
```

---

### Task 5: Implement the Authenticated gRPC Gateway

**Files:**
- Create: `pkg/finops/gateway/auth.go`
- Create: `pkg/finops/gateway/auth_test.go`
- Create: `pkg/finops/gateway/server.go`
- Create: `pkg/finops/gateway/server_test.go`
- Create: `pkg/finops/gateway/stream.go`
- Create: `pkg/finops/gateway/stream_test.go`
- Create: `pkg/finops/gateway/metrics.go`
- Create: `pkg/finops/gateway/metrics_test.go`

**Interfaces:**
- Consumes: Task 1 generated server interface, Task 2 Store, and Task 4 bootstrap service.
- Produces: `gateway.New(config Config) (*grpc.Server, *Server, error)` and a bufconn-tested FinOps Gateway.

- [ ] **Step 1: Write failing authentication and stream tests**

Cover these cases with `bufconn` and generated clients:

- `RegisterCluster` succeeds with server TLS plus a valid bearer token and no client cert.
- Missing/invalid bearer token is `Unauthenticated`.
- `Connect` and `RotateCertificate` without a verified client cert are `Unauthenticated`.
- A cert for cluster A cannot claim cluster B in `AgentMessage`.
- A rotated old certificate remains valid for 10 minutes, then is rejected.
- The first stream message must be `ClusterHello`.
- ACK is absent if Store returns an error and present only after Store success.
- Duplicate batch returns the same sequence with `duplicate=true`.
- Desired State newer than `ClusterHello.last_desired_state_revision` is sent after hello.
- A stale epoch is rejected with `FailedPrecondition`.

- [ ] **Step 2: Run tests and confirm missing gateway failures**

Run: `go test ./pkg/finops/gateway -count=1`

Expected: FAIL because gateway does not exist.

- [ ] **Step 3: Implement method-specific authentication**

Use a TLS config with `MinVersion: tls.VersionTLS13`, server certificate, `ClientCAs`, and `ClientAuth: tls.VerifyClientCertIfGiven`. Unary and stream interceptors enforce:

```go
const (
    registerMethod = "/finops.crane.io.v1.FinOpsGateway/RegisterCluster"
    rotateMethod   = "/finops.crane.io.v1.FinOpsGateway/RotateCertificate"
    connectMethod  = "/finops.crane.io.v1.FinOpsGateway/Connect"
)
```

Only `registerMethod` permits no peer cert. Parse exactly one verified client certificate and one
Crane URI SAN. Put `model.Identity` and certificate serial into context. Before accepting Rotate
or Connect, call `Store.AuthorizeCertificate` so the current serial and a previous serial within
its 10-minute rotation overlap are accepted.

- [ ] **Step 4: Implement Register and Rotate RPCs**

`RegisterCluster` reads one `authorization` metadata value, strips the exact `Bearer ` prefix,
validates request IDs/UID/versions, calls `bootstrap.Service.Register`, and returns
PEM/serial/expiry plus the Store's epoch floor. `RotateCertificate` takes identity and previous serial only from authenticated
context and calls `Service.Rotate`; the previous serial remains authorized only for the fixed
overlap window.

Map typed errors to stable gRPC codes: invalid input `InvalidArgument`, token/cert failure `Unauthenticated`, tenant/cluster mismatch `PermissionDenied`, conflict `AlreadyExists`, stale epoch `FailedPrecondition`, and storage outage `Unavailable`. Error messages never include tokens, certificate bodies, payloads, or SQL.

- [ ] **Step 5: Implement Connect handshake, ingestion, and Desired State**

```go
type Server struct {
    finopsv1.UnimplementedFinOpsGatewayServer
    Store          store.Store
    Bootstrap      *bootstrap.Service
    ServerInstance string
    Now            func() time.Time
}
```

Receive `ClusterHello` first, require message identity to match the authenticated identity, generate
a random connection ID, and call `OpenSession`. For each Inventory/Usage message, call the
canonical `finopsv1.VerifyInventoryChecksum` or `VerifyUsageChecksum`, deterministic-marshal the
full typed body for persistence, convert to `model.Batch`, call `StoreBatch` with connection ID,
then send `ServerAck`. Heartbeats and session close also include connection ID; a replaced stream
terminates on the first fenced Store result. Desired ACK updates only the exact revision/checksum.

Run send and receive loops under one `errgroup.WithContext`. Call `AuthorizeSession` before every
downstream message. Send current Desired State immediately after hello when newer, and again when
a 5-second polling ticker observes a higher revision. This polling is Phase 1's cross-replica
mechanism; a later phase may replace it without changing the protocol.

- [ ] **Step 6: Add bounded-cardinality metrics**

Register counters/gauges/histograms with only `result`, `message_kind`, and `grpc_code` labels:

```text
crane_finops_gateway_connections
crane_finops_gateway_registrations_total
crane_finops_gateway_batches_total
crane_finops_gateway_batch_bytes
crane_finops_gateway_ack_lag
crane_finops_gateway_auth_failures_total
```

Do not label metrics by tenant, cluster, plan, namespace, or resource.

- [ ] **Step 7: Run gateway tests and security searches**

Run:

```bash
go test -race ./pkg/finops/gateway -count=1
rg -n 'Printf|Infof|Errorf|InfoS|ErrorS' pkg/finops/gateway pkg/finops/bootstrap
```

Expected: tests PASS; every logging call shown by `rg` uses only identity IDs, method, result code, sequence, or sanitized error class, never token/cert/payload values.

- [ ] **Step 8: Commit the gateway**

```bash
git add pkg/finops/gateway
git commit -m "feat: serve authenticated finops agent streams"
```

---

### Task 6: Add the Central `crane-server` Binary and Admin CLI

**Files:**
- Create: `cmd/crane-server/main.go`
- Create: `cmd/crane-server/app/options.go`
- Create: `cmd/crane-server/app/options_test.go`
- Create: `cmd/crane-server/app/command.go`
- Create: `cmd/crane-server/app/command_test.go`
- Create: `cmd/crane-server/app/server.go`
- Create: `cmd/crane-server/app/server_test.go`
- Modify: `Makefile`
- Create: `Dockerfile.crane-server`

**Interfaces:**
- Consumes: MySQL Store, X.509 issuer, bootstrap service, and gRPC gateway.
- Produces: `crane-server serve`, `crane-server tenant create`, `crane-server cluster bootstrap`,
  `crane-server cluster rebootstrap`, `crane-server cluster revoke`, `/healthz`, `/readyz`, and
  `/metrics`.

- [ ] **Step 1: Write failing option and command tests**

Test that Serve rejects missing DSN/TLS/signer files, refuses identical server and client signing
keys, accepts ports 1-65535, validates certificate duration 1 hour to 30 days, prints a bootstrap
token only to command stdout, never prints the token to logs/stderr, and that rebootstrap/revoke
require an existing tenant/cluster while preserving its historical batches.

```go
func TestBootstrapCommandPrintsTokenOnce(t *testing.T) {
    stdout, stderr, err := executeCommand(t,
        "cluster", "bootstrap", "--tenant-id=tenant-a", "--cluster-id=cluster-a", "--ttl=15m")
    require.NoError(t, err)
    require.Regexp(t, `^[A-Za-z0-9_-]{43}\n$`, stdout)
    require.NotContains(t, stderr, strings.TrimSpace(stdout))
}
```

- [ ] **Step 2: Run tests and confirm missing binary failures**

Run: `go test ./cmd/crane-server/app -count=1`

Expected: FAIL because command and options do not exist.

- [ ] **Step 3: Implement validated options**

```go
type Options struct {
    HTTPAddress        string
    GRPCAddress        string
    DatabaseDSNFile    string
    DatabaseTLSCAFile  string
    DatabaseServerName string
    ServerTLSCertFile  string
    ServerTLSKeyFile   string
    ClientCACertFile   string
    ClientCAKeyFile    string
    ClientCertValidity time.Duration
    ShutdownTimeout    time.Duration
    ServerInstance     string
}
```

Defaults: HTTP `:8080`, gRPC `:8443`, client cert validity `24h`, shutdown `15s`, server instance
from hostname plus random suffix. Read DSN and key material from files only; trim one trailing
newline from DSN. Production Serve requires a database CA file and database ServerName. Parse the
DSN with `mysql.ParseDSN`, reject an existing `tls` query value, register a
`tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: configuredName}` under a
process-unique name, and assign that name to `mysql.Config.TLSConfig`. Never accept DSN, private
keys, or bootstrap tokens as flags.

Expose exact flags `--http-address`, `--grpc-address`, `--database-dsn-file`,
`--database-tls-ca-file`, `--database-server-name`, `--server-tls-cert-file`,
`--server-tls-key-file`, `--client-ca-cert-file`, `--client-ca-key-file`,
`--client-cert-validity`, `--shutdown-timeout`, and `--server-instance`. The three admin commands
use the same database DSN/CA/ServerName flags as Serve.

- [ ] **Step 4: Implement Serve lifecycle and health semantics**

`Run(ctx, Options)` opens MySQL, sets `SetMaxOpenConns(50)`, `SetMaxIdleConns(10)`, `SetConnMaxLifetime(5*time.Minute)`, pings, migrates, constructs the issuer/gateway, and starts HTTP and gRPC in one `errgroup`. `/healthz` reports process liveness. `/readyz` returns 200 only after migrations complete and a fresh DB ping succeeds within 2 seconds. `/metrics` uses the default Prometheus registry. On context cancellation, stop accepting gRPC connections, allow 10 seconds for graceful stop, then force stop; shut down HTTP and close DB.

- [ ] **Step 5: Implement admin commands**

Use Cobra hierarchy:

```text
crane-server serve
crane-server tenant create --tenant-id=tenant-a
crane-server cluster bootstrap --tenant-id=tenant-a --cluster-id=cluster-a --ttl=15m
crane-server cluster rebootstrap --tenant-id=tenant-a --cluster-id=cluster-a --ttl=15m
crane-server cluster revoke --tenant-id=tenant-a --cluster-id=cluster-a
```

All commands share `--database-dsn-file`, `--database-tls-ca-file`, and
`--database-server-name`. Tenant creation is idempotent only when the exact tenant
already exists; cluster bootstrap rejects an already registered cluster, revokes prior unused
tokens for that scope, and prints only the newly created token. `cluster rebootstrap` requires an
existing cluster and creates a token with `replaceIdentity=true`; redeeming it preserves historical
data, revokes prior certificates, and fences active sessions.
`cluster revoke` calls `RevokeClusterIdentity`, prints only the cluster scope, and is idempotent
for an already revoked cluster.

- [ ] **Step 6: Add build and image targets**

Add `CRANE_SERVER_IMG`, `crane-server`, `image-crane-server`, and corresponding push target to
`Makefile`; include `crane-server` in `build`, `all`, `images`, and `push-images`. Create a
dedicated `Dockerfile.crane-server` using the Go 1.25 builder and Alpine 3.22 runtime, copying only
`/crane-server`, installing CA certificates/timezone data, and setting `USER 65532:65532`. Do not
change the generic Dockerfile because the node-level `crane-agent` requires host-level access.

- [ ] **Step 7: Build and test the central binary**

Run:

```bash
go test -race ./cmd/crane-server/app -count=1
make crane-server
./bin/crane-server --help
```

Expected: tests PASS, build succeeds, and help lists `serve`, `tenant`, and `cluster`; cluster help
lists `bootstrap`, `rebootstrap`, and `revoke`.

- [ ] **Step 8: Commit the central binary**

```bash
git add cmd/crane-server Makefile Dockerfile.crane-server
git commit -m "feat: add central crane server"
```

---

### Task 7: Persist Cluster Identity and Register/Rotate from `craned`

**Files:**
- Create: `pkg/clusteragent/identity/store.go`
- Create: `pkg/clusteragent/identity/kubernetes.go`
- Create: `pkg/clusteragent/identity/kubernetes_test.go`
- Create: `pkg/clusteragent/client/client.go`
- Create: `pkg/clusteragent/client/client_test.go`
- Create: `pkg/clusteragent/client/tls.go`
- Create: `pkg/clusteragent/client/tls_test.go`

**Interfaces:**
- Consumes: generated FinOps client, server-root CA file, bootstrap token file, and a Kubernetes client.
- Produces: Secret-backed `identity.Store`, `client.EnsureIdentity`, `client.RotateIfNeeded`, and authenticated `client.Dial`.

- [ ] **Step 1: Write failing Secret and registration tests**

Use controller-runtime's fake client. Verify create/load/update of an immutable identity scope,
`0600`-equivalent Secret data semantics, no private-key bytes in errors, registration when Secret
is missing, reuse after restart without bootstrap token, refusal when Secret tenant/cluster differs
from configured identity, rotation before expiry, and concurrent monotonic epoch allocation.

```go
func TestKubernetesStoreRejectsIdentityScopeChange(t *testing.T) {
    subject := newFakeStore(t)
    require.NoError(t, subject.Save(ctx, fixtureIdentity("tenant-a", "cluster-a")))
    err := subject.Save(ctx, fixtureIdentity("tenant-a", "cluster-b"))
    require.ErrorIs(t, err, identity.ErrScopeMismatch)
}
```

- [ ] **Step 2: Run tests and confirm missing client failures**

Run: `go test ./pkg/clusteragent/identity ./pkg/clusteragent/client -count=1`

Expected: FAIL because packages do not exist.

- [ ] **Step 3: Define and implement Secret-backed identity**

```go
type Material struct {
    Identity       model.Identity
    PrivateKeyPEM  []byte
    CertificatePEM []byte
    ServerCAPEM    []byte
    SerialNumber   string
    ExpiresAt      time.Time
    Epoch          uint64
}

type Store interface {
    Load(context.Context) (Material, error)
    Save(context.Context, Material) error
    NextEpoch(context.Context) (uint64, error)
}
```

`KubernetesStore` uses Secret `crane-finops-identity` in `crane-system` by default with keys `tenant-id`, `cluster-id`, `tls.key`, `tls.crt`, `server-ca.crt`, `serial-number`, and `expires-at`. It uses optimistic concurrency on update, sets labels `app.kubernetes.io/managed-by=craned` and `finops.crane.io/identity=true`, and never changes scope after creation.

Also persist decimal key `epoch`. Initial registration saves the response `epoch_floor` before a
connection starts. `NextEpoch` retries Kubernetes conflicts, treats a missing value as zero,
increments by one, rejects overflow, updates the Secret, and returns the committed value.
Certificate Save preserves the current epoch. Two concurrent callers must return distinct
monotonically increasing values.

- [ ] **Step 4: Implement registration and mTLS dialing**

```go
type Config struct {
    ServerAddress     string
    ServerName        string
    Identity          model.Identity
    ClusterUID        string
    AgentVersion      string
    KubernetesVersion string
    Capabilities      []string
    ServerCAPEM       []byte
    BootstrapToken    string
    RotateBefore      time.Duration
}

func EnsureIdentity(ctx context.Context, cfg Config, identities identity.Store) (identity.Material, error)
func RotateIfNeeded(ctx context.Context, cfg Config, identities identity.Store, now time.Time) (identity.Material, error)
func Dial(ctx context.Context, cfg Config, material identity.Material) (*grpc.ClientConn, error)
```

Generate an ECDSA P-256 key and CSR locally. Registration uses server-auth TLS and bearer metadata; authenticated dialing uses the saved client certificate. `RotateIfNeeded` rotates when expiry is within `RotateBefore` (default 6 hours), calls the authenticated Rotate RPC with a new CSR, and atomically saves the new key/cert. Use `tls.VersionTLS13`; do not expose an insecure-skip-verify option.

On registration, save `CertificateResponse.epoch_floor` before `NextEpoch` is called. On routine
certificate rotation, preserve the Secret's existing epoch and ignore the response epoch floor.

- [ ] **Step 5: Run tests against a bufconn TLS gateway**

Run: `go test -race ./pkg/clusteragent/identity ./pkg/clusteragent/client -count=1`

Expected: PASS, including restart without token, response-loss rotation retry during overlap, and
old-serial rejection after overlap.

- [ ] **Step 6: Commit cluster identity client**

```bash
git add pkg/clusteragent/identity pkg/clusteragent/client
git commit -m "feat: register craned with finops server"
```

---

### Task 8: Implement Reliable Reporter, Resume, and Basic Inventory

**Files:**
- Create: `pkg/clusteragent/reporter/outbox.go`
- Create: `pkg/clusteragent/reporter/outbox_test.go`
- Create: `pkg/clusteragent/reporter/inventory.go`
- Create: `pkg/clusteragent/reporter/inventory_test.go`
- Create: `pkg/clusteragent/reporter/reporter.go`
- Create: `pkg/clusteragent/reporter/reporter_test.go`
- Create: `pkg/clusteragent/reporter/metrics.go`
- Create: `pkg/clusteragent/reporter/metrics_test.go`

**Interfaces:**
- Consumes: Task 7 client connection, controller-runtime Kubernetes client, and FinOps protocol.
- Produces: leader-election-aware `reporter.Reporter`, bounded ordered outbox, initial Node/Namespace InventoryDelta, heartbeat, reconnect/resume, and Desired State ACK without plan execution.

- [ ] **Step 1: Write failing outbox and reconnect tests**

Cover contiguous sequence allocation, ACK eviction, rejection when full, reconnect hello carrying the last ACK, resend of unacknowledged messages, duplicate ACK handling, desired revision ACK, heartbeat interval, and context cancellation. Use a fake clock and a scripted in-memory stream; do not use sleeps.

```go
func TestOutboxResendsOnlyUnacknowledgedMessages(t *testing.T) {
    outbox := NewOutbox(3)
    first := outbox.Enqueue(fixtureInventory())
    second := outbox.Enqueue(fixtureInventory())
    require.NoError(t, outbox.Ack(first.Sequence))
    pending := outbox.PendingAfter(first.Sequence)
    require.Equal(t, []uint64{second.Sequence}, sequences(pending))
}
```

- [ ] **Step 2: Run tests and confirm missing reporter failures**

Run: `go test ./pkg/clusteragent/reporter -count=1`

Expected: FAIL because reporter does not exist.

- [ ] **Step 3: Implement the bounded ordered outbox**

```go
var ErrOutboxFull = errors.New("finops reporter outbox full")

type Envelope struct { Sequence uint64; Message *finopsv1.AgentMessage }
type Outbox struct { /* mutex, capacity, nextSequence, acked, ordered entries */ }

func NewOutbox(capacity int) *Outbox
func (o *Outbox) Enqueue(message *finopsv1.AgentMessage) (Envelope, error)
func (o *Outbox) Ack(sequence uint64) error
func (o *Outbox) PendingAfter(sequence uint64) []Envelope
func (o *Outbox) Depth() int
```

Capacity defaults to 256. Do not drop or overwrite unacknowledged entries. Phase 1 inventory is recomputable, so restart starts a new epoch and full snapshot rather than persisting the outbox.

- [ ] **Step 4: Implement basic Node/Namespace inventory source**

```go
type InventorySource struct {
    Client       client.Client
    AllowedLabels map[string]struct{}
    Now          func() time.Time
}

func (s *InventorySource) Snapshot(ctx context.Context) (*finopsv1.InventoryDelta, error)
```

List Nodes and Namespaces, convert UID/APIVersion/Kind/Name/ResourceVersion, include only exact
allowed label keys, sort by Kind/Namespace/Name/UID, and call
`finopsv1.SetInventoryChecksum`. Mark `full_snapshot=true`, schema version 1, and use one instant
as a one-nanosecond non-empty window. Phase 2 replaces this with informer deltas and full workload
inventory.

- [ ] **Step 5: Implement Reporter connection loop**

```go
type Config struct {
    Identity          model.Identity
    HeartbeatInterval time.Duration
    InventoryInterval time.Duration
    ReconnectMin      time.Duration
    ReconnectMax      time.Duration
    OutboxCapacity    int
}

type Reporter struct {
    Config    Config
    Dial      func(context.Context) (finopsv1.FinOpsGateway_ConnectClient, io.Closer, error)
    NextEpoch func(context.Context) (uint64, error)
    Inventory *InventorySource
    Outbox    *Outbox
    Now       func() time.Time
}

func (r *Reporter) Start(ctx context.Context) error
func (r *Reporter) NeedLeaderElection() bool { return true }
```

After leader election invokes `Start`, call `NextEpoch` exactly once and retain the returned epoch
across transport reconnects. Send hello first, then pending entries. One receive loop processes ACK
and Desired State. Desired State is
validated for monotonic revision and checksum, stored only in reporter memory in Phase 1, and
ACKed; no plan is executed. Reconnect uses exponential backoff from 1 second to 1 minute with full
jitter. Inventory source errors leave the prior stream alive and increment a metric; transport
errors reconnect.

- [ ] **Step 6: Add bounded local Agent metrics**

Register metrics without tenant, cluster, namespace, or resource labels:

```text
crane_finops_agent_connected
crane_finops_agent_identity_valid
crane_finops_agent_last_ack_sequence
crane_finops_agent_outbox_depth
crane_finops_agent_errors_total{class}
```

The only allowed error classes are `configuration`, `registration`, `authentication`, `transport`,
`protocol`, `inventory`, and `outbox`. Tests gather each metric and assert that no descriptor
contains tenant or cluster IDs.

- [ ] **Step 7: Run race tests**

Run: `go test -race ./pkg/clusteragent/reporter -count=1`

Expected: PASS with no timers or goroutines left running after context cancellation.

- [ ] **Step 8: Commit reporter foundation**

```bash
git add pkg/clusteragent/reporter
git commit -m "feat: report cluster inventory to finops server"
```

---

### Task 9: Integrate FinOps Agent into `craned` Behind a Feature Gate

**Files:**
- Modify: `pkg/features/features.go`
- Modify: `cmd/craned/app/options/options.go`
- Create: `cmd/craned/app/options/finops.go`
- Create: `cmd/craned/app/options/finops_test.go`
- Modify: `cmd/craned/app/manager.go`
- Create: `cmd/craned/app/finops.go`
- Create: `cmd/craned/app/finops_test.go`
- Modify: `deploy/craned/rbac.yaml`

**Interfaces:**
- Consumes: controller-runtime manager, Task 7 identity/client, and Task 8 Reporter.
- Produces: disabled-by-default `FinOpsAgent` feature gate and one leader-elected outbound reporter per cluster.

- [ ] **Step 1: Write failing feature/config tests**

Verify default disabled behavior needs no flags or Secret access; enabled behavior requires server
address/name, tenant, cluster, and server CA. Bootstrap token is optional at static validation time
because an existing identity Secret can restart without it; the Reporter emits a sanitized metric
and retries when both identity and token are absent. Reject report intervals below 1 minute,
heartbeat below 10 seconds, and identity Secret names outside DNS subdomain rules.

```go
func TestFinOpsConfigDisabledByDefault(t *testing.T) {
    opts := NewOptions()
    require.NoError(t, opts.FinOps.Complete())
    require.Empty(t, opts.FinOps.Validate())
}
```

- [ ] **Step 2: Run focused tests and confirm missing feature failure**

Run: `go test ./cmd/craned/app/options ./cmd/craned/app -run FinOps -count=1`

Expected: FAIL because FinOps options and setup do not exist.

- [ ] **Step 3: Add feature gate and exact flags**

Add:

```go
CraneFinOpsAgent featuregate.Feature = "FinOpsAgent"
```

with `{Default: false, PreRelease: featuregate.Alpha}`.

```go
type FinOpsOptions struct {
    ServerAddress       string
    ServerName          string
    TenantID            string
    ClusterID           string
    ClusterUID          string
    ServerCAFile        string
    BootstrapTokenFile  string
    IdentitySecretName  string
    HeartbeatInterval   time.Duration
    InventoryInterval   time.Duration
    OutboxCapacity      int
}
```

Flags use `--finops-server-address`, `--finops-server-name`, `--finops-tenant-id`, `--finops-cluster-id`, `--finops-cluster-uid`, `--finops-server-ca-file`, `--finops-bootstrap-token-file`, `--finops-identity-secret-name`, `--finops-heartbeat-interval`, `--finops-inventory-interval`, and `--finops-outbox-capacity`. Defaults: Secret `crane-finops-identity`, heartbeat 30 seconds, inventory 5 minutes, outbox 256.

- [ ] **Step 4: Wire Reporter into the controller-runtime manager**

`setupFinOpsAgent(ctx, mgr, opts)` returns immediately when the gate is disabled. When enabled, it
builds the Secret identity Store and a Reporter whose leader-elected run loop performs registration,
rotation, and connection retries asynchronously, then calls `mgr.Add(reporter)`. Because
`Reporter.NeedLeaderElection()` returns true, only the current `craned` leader connects.

Do not add the remote center to `craned` global readiness or liveness. Expose independent bounded
metrics `crane_finops_agent_connected`, `crane_finops_agent_identity_valid`,
`crane_finops_agent_last_ack_sequence`, and `crane_finops_agent_outbox_depth`; center outage or
missing bootstrap material must never stop local controllers, webhooks, EHPA, Recommendation, or
the existing API.

- [ ] **Step 5: Restrict identity Secret RBAC**

Retain the existing broad `create` needed by current code. Add `get`, `patch`, and `update` only for resource name `crane-finops-identity`. Do not grant Secret list/watch.

- [ ] **Step 6: Run existing and new craned tests/build**

Run:

```bash
go test -race ./cmd/craned/app/... ./pkg/clusteragent/... -count=1
make craned
```

Expected: PASS; `craned` starts with existing defaults without FinOps flags.

- [ ] **Step 7: Commit craned integration**

```bash
git add pkg/features/features.go cmd/craned/app deploy/craned/rbac.yaml
git commit -m "feat: connect craned to finops server"
```

---

### Task 10: Add Production Manifests, Build Matrix, and Generated-Code CI

**Files:**
- Create: `deploy/crane-server/kustomization.yaml`
- Create: `deploy/crane-server/namespace.yaml`
- Create: `deploy/crane-server/serviceaccount.yaml`
- Create: `deploy/crane-server/deployment.yaml`
- Create: `deploy/crane-server/service.yaml`
- Create: `deploy/crane-server/networkpolicy.yaml`
- Create: `deploy/crane-server/README.md`
- Modify: `.github/workflows/go.yml`
- Modify: `.github/workflows/kubernetes-compatibility.yml`

**Interfaces:**
- Consumes: `crane-server` flags and ports from Task 6.
- Produces: two-replica central deployment with external MySQL secrets and CI that verifies protocol generation and both binaries.

- [ ] **Step 1: Add failing manifest assertions**

Extend the Kubernetes compatibility workflow or its existing validation script to assert:

```bash
kubectl kustomize deploy/crane-server > /tmp/crane-server.yaml
rg -q 'replicas: 2' /tmp/crane-server.yaml
rg -q 'runAsNonRoot: true' /tmp/crane-server.yaml
rg -q 'readOnlyRootFilesystem: true' /tmp/crane-server.yaml
rg -q 'allowPrivilegeEscalation: false' /tmp/crane-server.yaml
rg -q 'seccompProfile:' /tmp/crane-server.yaml
```

Run: `kubectl kustomize deploy/crane-server`

Expected: FAIL because manifests do not exist.

- [ ] **Step 2: Add central manifests**

Use namespace `crane-finops-system`, Service ports `8443` named `grpc` and `8080` named `http`, two replicas, rolling update `maxUnavailable: 0`, pod anti-affinity by hostname, readiness `/readyz`, liveness `/healthz`, 30-second termination grace, and a PodDisruptionBudget with `minAvailable: 1`.

Mount read-only Secrets:

```text
crane-server-database   keys dsn, ca.crt
crane-server-tls        keys tls.crt, tls.key
crane-cluster-client-ca keys ca.crt, ca.key
```

Run the container with:

```text
/crane-server serve
--database-dsn-file=/etc/crane/database/dsn
--database-tls-ca-file=/etc/crane/database/ca.crt
--database-server-name=mysql.crane-finops-system.svc
--server-tls-cert-file=/etc/crane/server-tls/tls.crt
--server-tls-key-file=/etc/crane/server-tls/tls.key
--client-ca-cert-file=/etc/crane/client-ca/ca.crt
--client-ca-key-file=/etc/crane/client-ca/ca.key
```

The checked-in Kustomize base sets database ServerName to
`mysql.crane-finops-system.svc`; operators using managed MySQL override only that argument and the
database Secret, not the container image.

Use an `emptyDir` only for `/tmp`. Set CPU request 250m, memory request 256Mi, CPU limit 2, memory
limit 1Gi. The NetworkPolicy permits gRPC/HTTP ingress, DNS egress, and TCP 3306 egress. README
requires production operators to tighten MySQL egress to the database CIDR through an overlay;
the checked-in port restriction remains functional for managed external MySQL endpoints.

- [ ] **Step 3: Update Go CI and build matrix**

Add `buf.yaml`, `buf.gen.yaml`, `deploy/**`, and `hack/finops/**` to workflow paths. Add `crane-server` to the build matrix. Before tests run:

```bash
make finops-proto
git diff --exit-code
```

This proves generated code is current. Add MySQL 8.4 as a service container and run `pkg/finops/store/mysql` tests with `CRANE_FINOPS_MYSQL_DSN`.

- [ ] **Step 4: Validate manifests and build image**

Run:

```bash
kubectl kustomize deploy/crane-server > /tmp/crane-server.yaml
kubectl apply --dry-run=client -f /tmp/crane-server.yaml
make crane-server
docker build -f Dockerfile.crane-server -t crane/crane-server:phase1 .
```

Expected: render, dry-run, build, and image build all PASS.

- [ ] **Step 5: Commit deployment and CI**

```bash
git add deploy/crane-server .github/workflows/go.yml .github/workflows/kubernetes-compatibility.yml
git commit -m "deploy: add central crane server"
```

---

### Task 11: Add a Three-Cluster End-to-End Recovery Test

**Files:**
- Create: `hack/finops/kind-center.yaml`
- Create: `hack/finops/kind-workload.yaml`
- Create: `hack/finops/mysql-test.yaml`
- Create: `hack/finops/e2e.sh`
- Create: `hack/finops/verify.sql`
- Create: `hack/finops/e2eclient/main.go`
- Create: `hack/finops/loadtest/main.go`
- Modify: `.github/workflows/go.yml`

**Interfaces:**
- Consumes: images/manifests from Tasks 6, 9, and 10.
- Produces: reproducible proof that two managed clusters register, report inventory, reconnect to another center replica, deduplicate batches, and preserve tenant isolation.

- [ ] **Step 1: Write the failing E2E script assertions**

The script uses `set -euo pipefail`, creates only clusters named `crane-finops-center`, `crane-finops-workload-a`, and `crane-finops-workload-b`, records which ones it created, and deletes only those in `trap`. Initial assertions:

```bash
test "$(query_sql "SELECT COUNT(*) FROM clusters")" = "2"
test "$(query_sql "SELECT COUNT(DISTINCT cluster_id) FROM ingestion_batches WHERE kind='inventory')" = "2"
test "$(query_sql "SELECT COUNT(*) FROM bootstrap_tokens WHERE used_at IS NOT NULL")" = "2"
test "$(query_sql "SELECT COUNT(*) FROM agent_sessions WHERE last_acked_sequence > 0")" = "2"
```

Run: `hack/finops/e2e.sh`

Expected: FAIL before environment setup and image deployment are implemented.

- [ ] **Step 2: Build the isolated Kind topology**

Create one center Kind cluster with gRPC NodePort `30443` and two managed Kind clusters on the
shared Docker `kind` network. Deploy MySQL 8.4 only into the center test cluster, create server,
client-signing, and database test CAs at runtime under a `mktemp -d` directory, issue the gRPC
server certificate for DNS `crane-server.test`, and issue the MySQL server certificate for DNS
`mysql.crane-finops-system.svc`. Configure MySQL with `require_secure_transport=ON`. Never commit
keys.

Resolve the center control-plane container IP into shell variable `CENTER_NODE_IP` and patch each
managed `craned` Pod with `hostAliases: crane-server.test -> $CENTER_NODE_IP`. Configure
`--finops-server-address=crane-server.test:30443` and
`--finops-server-name=crane-server.test`.

- [ ] **Step 3: Bootstrap both clusters and verify inventory ACKs**

Create tenants `tenant-a` and `tenant-b`, generate one token per cluster through `crane-server cluster bootstrap`, store each token only in its managed cluster Secret, and wait for:

```text
crane_finops_gateway_connections 2
two registered clusters
two consumed tokens
inventory batches containing each cluster's Node and Namespace objects
last_acked_sequence >= 1 for both sessions
```

Query MySQL from its Pod using `verify.sql`; do not expose DB externally.

- [ ] **Step 4: Prove reconnect, deduplication, and isolation**

Scale center Server to two, record each active session's `connection_id` and server instance, delete
the owning Pod, and assert both Agents reconnect within 60 seconds with the same process epoch,
new connection IDs, changed server ownership, and incremented `reconnect_count`.

Implement `hack/finops/e2eclient/main.go` as a test-only generated-client program that reads a
cluster identity Secret exported into its temporary directory, sends one valid deterministic
InventoryDelta twice with the same epoch/sequence/checksum, and then attempts a message claiming
the other cluster. Assert the duplicate ACK has `duplicate=true`, row count does not change, and
the identity mismatch returns `PermissionDenied`. Query each tenant through Store fixtures and
require no cross-tenant rows.

- [ ] **Step 5: Verify the 50-cluster connection target**

Before starting the client, `e2e.sh` creates tenant `load-test` and invokes
`crane-server cluster bootstrap` inside the center Pod 50 times, writing JSON lines containing
only `tenantId`, `clusterId`, and the one-time token to a mode-0600 file in its temporary directory.
Implement `hack/finops/loadtest/main.go` with `--bootstrap-file` to read and delete that file after
registration, generate 50 private keys locally, open 50 concurrent mTLS streams to a two-replica
server, send hello plus one inventory batch per stream, wait for all ACKs, disconnect, and reconnect
all clients with the same epochs and new connections. It prints JSON containing `clusters`,
`acked`, `reconnected`, `duplicates`, and `elapsedMillis` and never prints tokens or keys.

Invoke it from `e2e.sh` while the center and MySQL are still running:

```bash
go run ./hack/finops/loadtest --bootstrap-file="$LOAD_BOOTSTRAP_FILE" --timeout=30s > /tmp/finops-load.json
jq -e '.clusters == 50 and .acked == 50 and .reconnected == 50 and .duplicates == 0' /tmp/finops-load.json
```

Expected JSON fields: `clusters=50`, `acked=50`, `reconnected=50`, `duplicates=0`, with process
exit 0. Query MySQL for the load-test `cluster_id LIKE 'load-%'` scope and require exactly 50
cluster rows, 50 inventory rows, and 50 active sessions in addition to the two E2E clusters.

- [ ] **Step 6: Run E2E twice for cleanup and idempotency**

Run:

```bash
hack/finops/e2e.sh
hack/finops/e2e.sh
```

Expected: both runs PASS and leave no `crane-finops-*` Kind clusters, containers, temporary keys, or host files.

- [ ] **Step 7: Add a non-default E2E CI job**

Add a `finops-e2e` job triggered on pull requests touching `pkg/finops/**`, `pkg/clusteragent/**`, `cmd/crane-server/**`, `cmd/craned/**`, or `deploy/crane-server/**`. Build local images, load them into all three Kind clusters, run the script once, and upload sanitized pod logs on failure. Logs are piped through a check that rejects bootstrap token values captured by the script.

- [ ] **Step 8: Commit E2E coverage**

```bash
git add hack/finops .github/workflows/go.yml
git commit -m "test: verify finops cs recovery end to end"
```

---

### Task 12: Add Operations Runbook and Complete Phase 1 Verification

**Files:**
- Create: `docs/operations/finops-cs-foundation.md`
- Modify: `README.md`
- Modify: `README_zh.md`
- Modify: `docs/superpowers/plans/2026-07-21-crane-finops-cs-foundation.md` (check completed boxes only while executing)

**Interfaces:**
- Consumes: all Phase 1 behavior.
- Produces: operator-ready bootstrap, rotation, HA, recovery, rollback, security, and upgrade procedures plus final verification evidence.

- [ ] **Step 1: Write the operations runbook**

Document exact commands for:

1. Creating MySQL database/user with least privilege.
2. Generating separate server TLS, client-signing, and database-trust CAs.
3. Creating `crane-server-database`, `crane-server-tls`, and `crane-cluster-client-ca` Secrets,
   with DSN free of a `tls` parameter and database CA in `crane-server-database/ca.crt`.
4. Running migrations through `crane-server serve` readiness.
5. Creating a tenant and one-time cluster token.
6. Installing the server CA/token into a managed cluster and enabling `FinOpsAgent=true`.
7. Confirming certificate serial, session epoch, ACK sequence, freshness, and basic inventory.
8. Rolling server TLS certificates and rotating Agent certificates with the 10-minute serial overlap.
9. Recovering from center outage, MySQL restore, stale Agent identity, and exhausted outbox.
10. Rolling center-first and Agent-first N/N-1 upgrades.
11. Disabling the feature gate without affecting local EHPA/Recommendation.
12. Revoking one cluster by invalidating its registered certificate serial.
13. Recovering a lost identity Secret through `cluster rebootstrap` while preserving historical
    batches and resuming from `epoch_floor + 1`.

State explicitly that deleting `crane-finops-identity` after the one-time token is consumed requires an administrator to issue a new bootstrap token.

- [ ] **Step 2: Add top-level documentation links**

Add a concise “Multi-cluster FinOps C/S foundation” entry to English and Chinese READMEs linking to the approved design and operations runbook. Do not claim OpenCost allocation, cloud billing, optimization execution, budgets, or Dashboard are implemented in Phase 1.

- [ ] **Step 3: Run generated-code, unit, race, build, and manifest verification**

Run fresh commands:

```bash
make finops-proto
git diff --exit-code -- pkg/finops/proto/v1/finops.pb.go pkg/finops/proto/v1/finops_grpc.pb.go
go test -race ./pkg/finops/... ./pkg/clusteragent/... ./cmd/crane-server/... ./cmd/craned/app/... -count=1
go vet ./pkg/finops/... ./pkg/clusteragent/... ./cmd/crane-server/... ./cmd/craned/app/...
make crane-server craned
kubectl kustomize deploy/crane-server > /tmp/crane-server.yaml
kubectl apply --dry-run=client -f /tmp/crane-server.yaml
```

Expected: every command exits 0.

- [ ] **Step 4: Run MySQL and E2E verification**

Run:

```bash
docker compose -f hack/finops/docker-compose.yaml up -d mysql
CRANE_FINOPS_MYSQL_DSN='crane:crane@tcp(127.0.0.1:33306)/crane_finops?parseTime=true&multiStatements=true' go test -race ./pkg/finops/store/mysql -count=1
hack/finops/e2e.sh
docker compose -f hack/finops/docker-compose.yaml down -v
```

Expected: MySQL contract and E2E (including its 50-cluster load test) PASS; compose resources are
removed.

- [ ] **Step 5: Run security and scope audit**

Run:

```bash
if rg -n 'InsecureSkipVerify|grpc.WithInsecure|credentials/insecure' pkg/finops pkg/clusteragent cmd/crane-server; then exit 1; fi
rg -n 'bootstrap.*token|PrivateKeyPEM|caKeyPEM|DatabaseDSN' pkg/finops pkg/clusteragent cmd/crane-server
if rg -n 'opencost|kafka|clickhouse|thanos' pkg/finops pkg/clusteragent cmd/crane-server deploy/crane-server; then exit 1; fi
git diff --check
```

Expected: the prohibited-pattern checks exit 0 because they find no matches; review the middle
search matches and confirm they are input/storage code or redaction tests, never logging/metrics;
`git diff --check` exits 0.

- [ ] **Step 6: Commit documentation and final Phase 1 evidence**

```bash
git add docs/operations/finops-cs-foundation.md README.md README_zh.md docs/superpowers/plans/2026-07-21-crane-finops-cs-foundation.md
git commit -m "docs: operate finops cs foundation"
```

---

## Phase 1 Completion Gate

Phase 1 is complete only when all twelve tasks and all checks below have current evidence:

- Generated protocol is reproducible and N/N-1 additive compatibility rules are documented.
- `RegisterCluster` is the only no-client-certificate method and consumes a one-time token.
- Client private keys never leave managed-cluster Secrets.
- Every MySQL access is tenant-scoped and the cross-tenant contract passes.
- ACK is emitted only after commit; duplicate and out-of-order batches preserve the contiguous high-water mark.
- Two managed clusters reconnect to another center replica within 60 seconds.
- The load harness connects and reconnects 50 concurrent clusters and ACKs one inventory batch
  from each within 30 seconds.
- Disabled `FinOpsAgent` preserves all current Crane behavior and requires no new Secret.
- Enabled `FinOpsAgent` sends basic Node/Namespace inventory but executes no plan.
- Center loss does not stop local Craned controllers, EHPA, HPA, Recommendation, or QoS.
- Production manifests satisfy Restricted Pod Security and do not deploy MySQL or OpenCost.
- Unit, race, vet, build, MySQL, manifest, security, and E2E commands all pass.
- The operator runbook covers bootstrap, rotation, recovery, revocation, upgrade, and rollback.

After this gate, create a separate Phase 2 plan for OpenCost-compatible Allocation, Asset, Idle, Shared Cost, full workload inventory, and UsageWindow collection. Do not add those behaviors to this plan.
