# Crane Project Capabilities Guide Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create `docs/project-capabilities.zh.md`, an evidence-based Chinese guide that lets technology and product evaluators judge Crane's current capabilities, dependencies, delivery boundaries, and adoption risks without reading the source code.

**Architecture:** Keep the deliverable in one standalone Markdown document organized by capability domain, with a summary matrix first and code evidence links last. Treat source code, Feature Gates, CRDs, deployment manifests, and tests as primary evidence; use the Understand Anything graph only to cross-check coverage, and resolve conflicts in favor of current source code.

**Tech Stack:** Markdown, Go 1.25 module, Kubernetes 1.35 dependencies, controller-runtime, custom-metrics-apiserver, Gin, React Dashboard, Helm/Kustomize manifests.

## Global Constraints

- The document is for technology and product selection evaluation, not a code-level onboarding guide.
- Save the final deliverable at `docs/project-capabilities.zh.md` and leave `docs/project-implementation-architecture.zh.md` unchanged.
- Analyze the current branch only; external repositories are dependencies, not repository-delivered capabilities.
- Mark every Feature Gate from `pkg/features/features.go` as Alpha and preserve its actual default value.
- Distinguish code existence, default enablement, standalone deployment, user-facing integration, and complete product closure.
- Every major capability must state its scenario, result, dependencies, enablement, delivery status, limits, and evidence.
- Do not include placeholders, unsupported marketing claims, or promises inferred only from proposals and roadmaps.
- Use relative repository links in the final document.

---

### Task 1: Establish the Evidence Baseline and Capability Matrix

**Files:**
- Create: `docs/project-capabilities.zh.md`
- Read: `docs/superpowers/specs/2026-07-19-project-capabilities-design.md`
- Read: `README_zh.md`
- Read: `docs/index.zh.md`
- Read: `pkg/features/features.go`
- Read: `cmd/craned/app/manager.go`
- Read: `cmd/crane-agent/app/agent.go`
- Read: `cmd/metric-adapter/main.go`
- Read: `cmd/cost-collector/main.go`
- Test: `docs/project-capabilities.zh.md` structural checks

**Interfaces:**
- Consumes: the approved status model and chapter structure from the design specification.
- Produces: document title, scope, evaluation method, status legend, executive summary, and the canonical capability matrix used by later tasks.

- [ ] **Step 1: Confirm the target file does not already exist**

Run:

```bash
test ! -e docs/project-capabilities.zh.md
```

Expected: exit code 0. If the file exists, stop and inspect it before making any change.

- [ ] **Step 2: Build a disposable Chinese knowledge graph for coverage checking**

Create an isolated local clone outside the working tree, then invoke the `understand-anything:understand` skill against that clone with `--language zh`. Follow its `.understandignore`, file-count, and review gates exactly.

Run:

```bash
git clone --no-hardlinks . /tmp/crane-capability-analysis
```

Expected: `/tmp/crane-capability-analysis` is a clean clone at the current commit. The skill must produce `/tmp/crane-capability-analysis/.understand-anything/knowledge-graph.json`; do not copy generated graph files into the working tree.

- [ ] **Step 3: Inventory executable processes and Feature Gates from primary evidence**

Run:

```bash
find cmd -maxdepth 3 -type f -name '*.go' | sort
rg -n 'featuregate.Feature|Default:' pkg/features/features.go
rg -n 'Enabled\(features\.' cmd pkg/agent pkg/ensurance pkg/metrics
```

Expected: four executable process entries (`craned`, `crane-agent`, `metric-adapter`, `cost-collector`) and ten Alpha Feature Gates, five default-on and five default-off.

- [ ] **Step 4: Write the document foundation and canonical status legend**

Create `docs/project-capabilities.zh.md` with these exact top-level sections and definitions:

```markdown
# Crane 项目能力与选型说明

## 执行摘要

## 评估范围与方法

## 状态说明

| 维度 | 状态 | 含义 |
|---|---|---|
| 启用方式 | 默认启用 | 对应能力的 Feature Gate 默认值为 `true`，仍需满足组件与数据源前提 |
| 启用方式 | 可选 Alpha | Feature Gate 默认值为 `false`，需要显式开启 |
| 启用方式 | 独立部署 | 作为单独进程部署，不受 `craned` Feature Gate 管理 |
| 启用方式 | 外部依赖 | 需要当前仓库之外的项目或服务 |
| 交付状态 | 已形成闭环 | 仓库内具备配置入口、处理链路和可消费结果 |
| 交付状态 | 部分闭环 | 核心处理已实现，但展示、控制、持久化或集成仍不完整 |
| 交付状态 | 底层能力 | 提供 API、指标或执行机制，需要上层系统组合 |
| 交付状态 | 外部能力 | 主要实现不在当前仓库 |

## 能力全景

## 多云成本与 FinOps

## 资源分析与优化推荐

## 弹性伸缩与时间序列预测

## QoS、节点资源与调度

## 平台与运维支撑

## 典型部署组合

## 选型结论与风险

## 代码证据索引
```

The capability matrix must contain these rows: multi-cloud bill collection, cost allocation and ledger, resource recommendation, replica recommendation, HPA recommendation, idle-node recommendation, Service recommendation, Volume recommendation, EHPA, EVPA, TimeSeriesPrediction, custom/external metrics, QoS avoidance, Crane CPU Manager, NodeResourceTopology, Dashboard/Web API, load-aware scheduling, and Fadvisor cost visibility.

- [ ] **Step 5: Verify foundation structure and terminology**

Run:

```bash
rg -n '^## (执行摘要|评估范围与方法|状态说明|能力全景|多云成本与 FinOps|资源分析与优化推荐|弹性伸缩与时间序列预测|QoS、节点资源与调度|平台与运维支撑|典型部署组合|选型结论与风险|代码证据索引)$' docs/project-capabilities.zh.md
rg -n '默认启用|可选 Alpha|独立部署|外部依赖|已形成闭环|部分闭环|底层能力|外部能力' docs/project-capabilities.zh.md
```

Expected: all twelve level-two headings and all eight canonical status terms are present.

- [ ] **Step 6: Commit the foundation**

```bash
git add docs/project-capabilities.zh.md
git diff --cached --check
git commit -m "docs: add crane capability evaluation overview"
```

Expected: one commit containing only the new capability guide foundation.

---

### Task 2: Document Cost and Recommendation Capabilities

**Files:**
- Modify: `docs/project-capabilities.zh.md`
- Read: `pkg/cost/service/service.go`
- Read: `pkg/cost/service/config.go`
- Read: `pkg/cost/collector/collector.go`
- Read: `pkg/cost/allocation/allocation.go`
- Read: `pkg/cost/reconcile/reconcile.go`
- Read: `pkg/cost/ledger/store.go`
- Read: `pkg/cost/ledger/file.go`
- Read: `pkg/cost/ratecard/catalog.go`
- Read: `pkg/cost/providers/register.go`
- Read: `pkg/cost/provider/aliyun/aliyun.go`
- Read: `pkg/cost/provider/aliyun/bills.go`
- Read: `pkg/cost/provider/huawei/huawei.go`
- Read: `pkg/cost/provider/huawei/bills.go`
- Read: `pkg/cost/provider/tencent/tencent.go`
- Read: `pkg/cost/provider/tencent/bills.go`
- Read: `pkg/cost/provider/volcengine/volcengine.go`
- Read: `pkg/cost/provider/volcengine/bills.go`
- Read: `pkg/recommendation/recommender/resource/registry.go`
- Read: `pkg/recommendation/recommender/replicas/registry.go`
- Read: `pkg/recommendation/recommender/hpa/registry.go`
- Read: `pkg/recommendation/recommender/idlenode/registry.go`
- Read: `pkg/recommendation/recommender/service/registry.go`
- Read: `pkg/recommendation/recommender/volume/registry.go`
- Read: `pkg/controller/recommendation/recommendation_controller.go`
- Read: `pkg/controller/recommendation/recommendation_rule_controller.go`
- Read: `pkg/controller/recommendation/recommendation_trigger_controller.go`
- Read: `pkg/controller/recommendation/recommendation_checker.go`
- Read: `pkg/controller/recommendation/updater.go`
- Read: `pkg/server/handler/recommendation/recommendation.go`
- Read: `deploy/cost-collector/deployment.yaml`
- Read: `deploy/cost-collector/configmap.yaml`
- Read: `deploy/cost-collector/pvc.yaml`
- Read: `charts/crane-cost-collector/values.yaml`
- Read: `charts/crane-cost-collector/templates/deployment.yaml`
- Test: `pkg/cost/...`, `pkg/recommendation/...`, `pkg/controller/recommendation/...`

**Interfaces:**
- Consumes: status terms and matrix rows defined in Task 1.
- Produces: complete `多云成本与 FinOps` and `资源分析与优化推荐` sections, plus corrected matrix statuses for these domains.

- [ ] **Step 1: Verify cost-provider registration and runtime outputs**

Run:

```bash
rg -n 'Register|aliyun|huawei|tencent|volcengine' pkg/cost/providers pkg/cost/provider
rg -n 'ListenAndServe|prometheus|ledger|allocation|rate.card|reconcil' pkg/cost/service pkg/cost
rg -n 'securityContext|readOnlyRootFilesystem|persistentVolume|config' charts/crane-cost-collector deploy/cost-collector
```

Expected: four cloud providers are registered; the service exposes health/metrics endpoints, writes a ledger, supports rate-card allocation and reconciliation, and has standalone Kubernetes deployment resources.

- [ ] **Step 2: Verify recommender registration, scheduling, and adoption paths**

Run:

```bash
rg -n 'RegisterRecommenderProvider|Name\(\) string' pkg/recommendation/recommender
rg -n 'Reconcile|Schedule|RunNumber|RecommendationRule|Recommendation' pkg/controller/recommendation
rg -n 'AdoptRecommendation|ListRecommendations|RecommendationRule' pkg/server
```

Expected: resource, replicas, HPA, idle-node, Service, and Volume recommenders are discoverable; RecommendationRule drives generation; the Web API exposes list/rule operations and an adoption endpoint.

- [ ] **Step 3: Write the two capability-domain sections**

The cost section must state:

- `cost-collector` is an independently deployed, read-only bill ingestion service.
- Aliyun, Huawei Cloud, Tencent Cloud, and Volcengine are implemented providers.
- The pipeline covers paginated ingestion, normalized bill records, idempotent ledger updates, rate-card allocation, reconciliation, health checks, and Prometheus metrics.
- Credentials, cloud billing API access, persistent ledger storage, and provider-specific configuration are mandatory dependencies.
- The repository does not connect this ledger to the existing Dashboard or `craned` APIs; therefore the capability is `部分闭环`, not a complete cost-visibility product.
- Fadvisor-based Kubernetes cost visibility is an `外部能力` and must not be conflated with the new bill collector.

The recommendation section must describe each registered recommender separately and must distinguish analysis from automated remediation. It must state that RecommendationRule schedules analysis, Recommendation stores results, the API can adopt supported recommendations, and UI/adoption coverage varies by type. Treat recommendation as `已形成闭环` only where generation and adoption are both evidenced; otherwise use `部分闭环`.

- [ ] **Step 4: Run targeted implementation-health tests**

Run:

```bash
go test ./pkg/cost/... ./pkg/recommendation/... ./pkg/controller/recommendation/...
```

Expected: exit code 0 with each listed package reporting `ok` or `[no test files]`. If failures occur, record them as evaluation risk; do not modify production code as part of this documentation task.

- [ ] **Step 5: Check required claims and provider names**

Run:

```bash
rg -n '阿里云|华为云|腾讯云|火山引擎|账本|分摊|对账|Fadvisor' docs/project-capabilities.zh.md
rg -n '资源推荐|副本推荐|HPA 推荐|闲置节点|Service|Volume|RecommendationRule|采纳' docs/project-capabilities.zh.md
```

Expected: every provider and recommender category appears with dependencies and delivery boundaries.

- [ ] **Step 6: Commit cost and recommendation analysis**

```bash
git add docs/project-capabilities.zh.md
git diff --cached --check
git commit -m "docs: assess cost and recommendation capabilities"
```

Expected: one documentation-only commit.

---

### Task 3: Document Autoscaling, Prediction, QoS, and Platform Capabilities

**Files:**
- Modify: `docs/project-capabilities.zh.md`
- Read: `pkg/controller/ehpa/effective_hpa_controller.go`
- Read: `pkg/controller/ehpa/hpa.go`
- Read: `pkg/controller/ehpa/predict.go`
- Read: `pkg/controller/evpa/effective_vpa_controller.go`
- Read: `pkg/controller/timeseriesprediction/time_series_prediction_controller.go`
- Read: `pkg/predictor/predictor.go`
- Read: `pkg/prediction/dsp/prediction.go`
- Read: `pkg/prediction/percentile/prediction.go`
- Read: `pkg/metricprovider/custom_metric_provider.go`
- Read: `pkg/metricprovider/external_metric_provider.go`
- Read: `pkg/providers/config.go`
- Read: `pkg/providers/prom/prom.go`
- Read: `pkg/providers/metricserver/metricserver.go`
- Read: `pkg/providers/grpc/grpc.go`
- Read: `pkg/ensurance/collector/collector.go`
- Read: `pkg/ensurance/analyzer/analyzer.go`
- Read: `pkg/ensurance/executor/schedule.go`
- Read: `pkg/ensurance/executor/throttle.go`
- Read: `pkg/ensurance/executor/evict.go`
- Read: `pkg/ensurance/runtime/runtime.go`
- Read: `pkg/ensurance/cm/cpumanager/cpu_manager.go`
- Read: `pkg/resource/node_resource_manager.go`
- Read: `pkg/resource/pod_resource_manger.go`
- Read: `pkg/topology/node_resource_topology.go`
- Read: `pkg/server/router.go`
- Read: `pkg/web/package.json`
- Read: `deploy/craned/deployment.yaml`
- Read: `deploy/crane-agent/daemonset.yaml`
- Read: `deploy/metric-adapter/deployment.yaml`
- Read: `deploy/metric-adapter/apiservice.yaml`
- Test: autoscaling, prediction, metric-provider, ensurance, resource, topology, and server packages

**Interfaces:**
- Consumes: matrix and canonical status terms from Task 1.
- Produces: complete autoscaling/prediction, QoS/node, and platform sections, with explicit runtime and external-project boundaries.

- [ ] **Step 1: Trace autoscaling and prediction consumers**

Run:

```bash
rg -n 'Owns|Watches|HorizontalPodAutoscaler|TimeSeriesPrediction|Substitute' pkg/controller/ehpa pkg/controller/evpa
rg -n 'crane_autoscaling_cron|crane_autoscaling_prediction|ExternalMetric|CustomMetric' pkg/metricprovider
rg -n 'DSP|Percentile|New.*Predictor|DataSource' pkg/predictor pkg/prediction pkg/providers
```

Expected: EHPA manages native HPA/TSP resources; cron and prediction are exposed through external metrics; DSP and percentile predictors consume configured metric providers.

- [ ] **Step 2: Trace node-side prerequisites and actions**

Run:

```bash
rg -n 'Disable|Throttle|Evict|Taint|cgroup|CRI|Runtime' pkg/ensurance
rg -n 'CraneCPUManager|NodeResourceTopology|NodeResource|PodResource' pkg/features pkg/agent pkg/resource pkg/topology
rg -n 'privileged|hostPID|hostPath|mountPath' deploy/crane-agent charts deploy/manifests
```

Expected: disable-scheduling, throttle, and eviction actions are implemented; CPU Manager and topology are default-off Alpha gates; agent deployment requires node-level host access.

- [ ] **Step 3: Verify platform-facing interfaces**

Run:

```bash
sed -n '1,180p' pkg/server/router.go
rg -n 'GET\(|POST\(|PUT\(|DELETE\(' pkg/server
rg -n 'scripts|dependencies|react|vite|tdesign|echarts' pkg/web/package.json
```

Expected: Web API routes cover cluster metadata, namespaces, recommendations, rules, Prometheus proxying, dashboards, and prediction debugging; no cost-ledger route is present.

- [ ] **Step 4: Write the remaining technical capability sections**

The autoscaling and prediction section must cover EHPA observation/cron/prediction policies, native HPA delegation, EVPA recommendations, TSP, DSP/percentile algorithms, Prometheus/metrics-server/gRPC/mock providers, and custom/external metric API behavior. Explicitly state that data quality and monitoring availability determine prediction usefulness.

The QoS and node section must cover metric collection, anomaly analysis, disable-scheduling/throttle/evict actions, NodeResource/PodResource reporting, optional CPU Manager, and optional NodeResourceTopology. State the privileged host/CRI/cgroup prerequisites and separate external `crane-scheduler` functionality from the current repository.

The platform section must cover Dashboard, Gin Web API, Prometheus self-metrics, validating/mutating webhooks, Feature Gates, CRDs, and deployment artifacts. State that the cluster API does not by itself prove full multi-cluster reconciliation, and that not every backend capability has a Dashboard workflow.

- [ ] **Step 5: Run targeted implementation-health tests**

Run:

```bash
go test ./pkg/controller/ehpa/... ./pkg/controller/evpa/... ./pkg/controller/timeseriesprediction/... ./pkg/predictor/... ./pkg/prediction/... ./pkg/metricprovider/... ./pkg/ensurance/... ./pkg/resource/... ./pkg/topology/... ./pkg/server/...
```

Expected: exit code 0 with each listed package reporting `ok` or `[no test files]`. Record any failure as a risk rather than changing production code.

- [ ] **Step 6: Check capability names, actions, and boundary language**

Run:

```bash
rg -n 'EHPA|EVPA|TimeSeriesPrediction|DSP|Percentile|Custom Metrics|External Metrics' docs/project-capabilities.zh.md
rg -n '禁调度|限流|驱逐|CPU Manager|NodeResourceTopology|CRI|cgroup|crane-scheduler' docs/project-capabilities.zh.md
rg -n 'Dashboard|Web API|Webhook|Prometheus|多集群' docs/project-capabilities.zh.md
```

Expected: all named capabilities appear, and each section includes prerequisites and limits.

- [ ] **Step 7: Commit autoscaling, QoS, and platform analysis**

```bash
git add docs/project-capabilities.zh.md
git diff --cached --check
git commit -m "docs: assess optimization and platform capabilities"
```

Expected: one documentation-only commit.

---

### Task 4: Add Deployment Combinations, Selection Conclusions, and Final Evidence

**Files:**
- Modify: `docs/project-capabilities.zh.md`
- Read: `Dockerfile`
- Read: `Makefile`
- Read: `deploy/craned/deployment.yaml`
- Read: `deploy/crane-agent/daemonset.yaml`
- Read: `deploy/metric-adapter/deployment.yaml`
- Read: `deploy/cost-collector/deployment.yaml`
- Read: `charts/crane-cost-collector/Chart.yaml`
- Read: `charts/crane-cost-collector/values.yaml`
- Read: `.github/workflows/go.yml`
- Read: `.github/workflows/kubernetes-compatibility.yml`
- Read: `docs/multi-cloud-cost-and-kubernetes-compatibility-assessment.zh.md`
- Read: `docs/multi-cloud-cost-and-kubernetes-implementation.zh.md`
- Test: final Markdown and evidence consistency checks

**Interfaces:**
- Consumes: all domain sections and matrix statuses from Tasks 1-3.
- Produces: deployment-combination table, selection conclusions, risk register, evidence index, and a fully validated standalone guide.

- [ ] **Step 1: Verify build, deployment, and compatibility evidence**

Run:

```bash
rg -n '^build:|^images:|craned:|crane-agent:|metric-adapter:|cost-collector:' Makefile
find deploy charts/crane-cost-collector -maxdepth 3 -type f | sort
rg -n 'kubernetes|matrix|go-version|kind|compat' .github/workflows/go.yml .github/workflows/kubernetes-compatibility.yml
```

Expected: build/image targets exist for all four backend processes plus Dashboard, and deployment/compatibility artifacts can be tied to the risk section without treating assessment documents as runtime proof.

- [ ] **Step 2: Add exact deployment combinations**

Add a table with these rows and required components:

| Combination | Required components |
|---|---|
| Recommendation baseline | `craned`, Crane CRDs, Prometheus, Dashboard optional |
| Predictive autoscaling | `craned`, `metric-adapter`, Crane CRDs, Prometheus or another configured data source |
| Node QoS enforcement | `craned`, privileged `crane-agent`, Crane CRDs, compatible CRI/cgroup environment |
| Multi-cloud bill collection | standalone `cost-collector`, cloud credentials, billing API access, persistent ledger volume, Prometheus optional |
| Full combined deployment | all repository components plus external monitoring; Fadvisor and crane-scheduler remain separate optional projects |

For each row, state what remains unavailable without optional Dashboard or external projects.

- [ ] **Step 3: Write selection conclusions and the risk register**

The conclusion must identify Crane as strongest for Kubernetes resource recommendation, predictive autoscaling, and node-side optimization where operators accept Alpha APIs and Prometheus-centered integration. It must identify incomplete cost-to-Dashboard integration, external scheduling/cost-visibility projects, privileged agent requirements, Feature Gate maturity, dependency-version alignment, credential handling, local file-ledger durability, and uneven UI coverage as explicit adoption risks.

- [ ] **Step 4: Add the evidence index and architecture cross-reference**

Map every capability domain to its primary source paths. End with a relative link to `[Crane 项目实现与能力架构分析](project-implementation-architecture.zh.md)` for readers who need code-level flows. Do not duplicate that document's Mermaid architecture or extension tutorials.

- [ ] **Step 5: Run full documentation validation**

Run:

```bash
git diff --check
rg -n 'T(O){2}D|T(B)D|待[补]充|后续[完]善|适当[处]理|相关[内]容' docs/project-capabilities.zh.md
rg -n '^## ' docs/project-capabilities.zh.md
test -f docs/project-implementation-architecture.zh.md
```

Expected: `git diff --check` exits 0; the placeholder scan returns no matches; all twelve required level-two headings exist; the architecture cross-reference target exists.

- [ ] **Step 6: Cross-check summary, matrix, and domain conclusions**

Read the complete document and verify these invariants:

- No capability marked `已形成闭环` is described later as lacking its only output or consumer.
- Every external repository is marked `外部能力` or `外部依赖` everywhere.
- Every default-off gate is called `可选 Alpha`; default-on gates are not described as stable.
- `cost-collector` is consistently `独立部署` and `部分闭环`.
- Deployment combinations use the same component names as the capability matrix.

Expected: no contradictions. Fix wording in `docs/project-capabilities.zh.md` if an invariant fails, then rerun Step 5.

- [ ] **Step 7: Commit the completed guide**

```bash
git add docs/project-capabilities.zh.md
git diff --cached --check
git commit -m "docs: complete crane capability selection guide"
```

Expected: final documentation-only commit with a clean working tree.
