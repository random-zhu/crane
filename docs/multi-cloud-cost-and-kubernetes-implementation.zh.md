# Crane 多云成本与 Kubernetes 1.34-1.36 实现说明

> 实现日期：2026-07-17  
> 实施基线：`2b0ddae`  
> 对应评估：[multi-cloud-cost-and-kubernetes-compatibility-assessment.zh.md](multi-cloud-cost-and-kubernetes-compatibility-assessment.zh.md)

## 1. 交付结论

当前工作区已经形成一套可独立运行的“成本只读”实现：一个进程可同时采集阿里云、火山引擎、华为云和腾讯云多个付款账号的账单，归一化写入本地持久化台账，通过 API 和 Prometheus 查询；它不依赖 crane-agent、EHPA、驱逐或聚合 API 权限。

同时，Crane 主 Go 代码已迁移到 Kubernetes 1.35.6 客户端依赖基线，并面向 1.34-1.36 服务端建立持续验证。运行时使用稳定的 `autoscaling/v2`、`policy/v1`、CRI v1；成本只读 Chart 明确声明 `>=1.34 <1.37`。

这次交付是可运行的接口级实现，不等于云厂商财务验收。四家云真实脱敏账单、ACK/VKE/CCE/TKE 托管集群、CRI-O 和节点重启场景仍需在具备账号与集群后补测。

## 2. 代码结构

| 路径 | 职责 |
| --- | --- |
| `cmd/cost-collector` | 独立进程入口、配置校验和优雅退出 |
| `pkg/cost/model` | 云无关 Provider、费率、账期、游标和账单行；金额使用精确十进制字符串 |
| `pkg/cost/provider` | 身份解析、费率、账单 API、账单文件和凭证契约及注册中心 |
| `pkg/cost/provider/{aliyun,volcengine,huawei,tencent}` | 四家云鉴权、分页、错误处理和字段归一化 |
| `pkg/cost/ratecard` | 合同价/自定义费率目录与按账号、区域、实例类型、付费模式匹配 |
| `pkg/cost/collector` | 按自然月至少一次采集、页游标、回看重采、故障隔离和页数熔断 |
| `pkg/cost/ledger` | 单进程、崩溃安全 JSON 台账；幂等覆盖、游标和精确汇总 |
| `pkg/cost/reconcile` | 云资源 ID 到 Kubernetes 集群、标签和成本中心的显式映射 |
| `pkg/cost/allocation` | 按权重下钻到集群/Namespace/Workload/Pod/成本中心，尾差守恒 |
| `pkg/cost/service` | 多账号编排、健康检查、查询/汇总/分摊 API 和 Prometheus 指标 |
| `deploy/cost-collector` | 固定名称的 Kustomize 成本只读部署 |
| `charts/crane-cost-collector` | Kubernetes 1.34-1.36 Helm Chart |

## 3. 云厂商能力

| Provider | 账单入口 | 身份与短期凭证 | 当前摊销口径 |
| --- | --- | --- | --- |
| 阿里云 | BSS `DescribeInstanceBill` | AK/SK，可选 STS `security_token` | 优先厂商 `AmortizedCost`，缺失时回退净成本 |
| 火山引擎 | Billing `ListAmortizedCostBillDetail` | AK/SK，可选 `session_token` | 直接采集明细摊销成本 |
| 华为云 | BSS `/v2/bills/customer-bills/res-records/query` | 轮换 `X-Auth-Token` 文件 | 资源消费记录无独立摊销字段时显式回退净成本 |
| 腾讯云 | Billing `DescribeBillDetail` | SecretId/SecretKey，可选临时 `token` | 优先摊销字段，缺失时回退实付成本 |

每次请求都会重新读取凭证文件，因此 Kubernetes Secret 投射、STS sidecar 或 token agent 更新文件后无需重启进程。适配器的 API 失败彼此隔离，任一云不可用不会阻塞其他云，也不会影响历史查询。

Provider 还提供 Node `providerID` 解析。费率能力当前使用配置注入的 `rate_card_json`，适合合同价和人工确认价；云厂商实时报价 API 自动同步尚未实现，不能把自定义费率称为厂商实时报价。

## 4. 数据与一致性语义

- 金额、用量、汇率相关值只接受 JSON 字符串十进制，禁止先转 `float64` 再入账。
- 幂等键优先使用厂商账单行 ID；缺失时对稳定身份字段和排序标签做哈希，金额变更会覆盖旧版本。
- 云 API 对网络错误、`408`、`429` 和 `5xx` 最多尝试四次，遵循受上限保护的 `Retry-After`，否则使用指数退避；默认 HTTP 超时为 30 秒。
- 页数据先落账，再保存下一页游标；进程在两步之间退出只会触发安全重放。
- 已完成账期会从第一页重新采集，用于吸收最近 3-7 天的延迟、退款和调账；单次超过 `maxPages` 会失败关闭，不会伪报成功。
- 汇总按 Provider、付款账号、集群和币种分组，禁止跨币种直接相加。
- 分摊对正费用和退款都保持 `原始 = 已分摊 + 未分摊`，最后一份接收十进制舍入尾差。

当前文件台账适合单副本 PoC 和中小规模只读接入。Chart 强制单副本和 PVC。大账单、多副本、审计归档和高并发查询应通过既有 `ledger.Store` 契约增加 PostgreSQL/ClickHouse，并实现 `BillFileSource` 对接 OSS/COS/OBS/TOS；这部分不应通过扩大 Prometheus 标签基数替代。

## 5. 配置与运行

直接构建：

```bash
make cost-collector
./bin/cost-collector --config ./config.json --validate-config
./bin/cost-collector --config ./config.json
```

Kustomize 部署：

```bash
kubectl apply -f deploy/cost-collector/namespace.yaml
kubectl apply -f secret.yaml
kubectl apply -k deploy/cost-collector
```

Helm 部署：

```bash
helm lint charts/crane-cost-collector
kubectl create namespace crane-cost-system
kubectl label namespace crane-cost-system pod-security.kubernetes.io/enforce=restricted
helm upgrade --install crane-cost charts/crane-cost-collector \
  --namespace crane-cost-system \
  --set image.tag=<immutable-release-tag>
```

生产前必须修改 `config.providers` 的账号、区域和启用列表，并由外部 Secret 管理系统创建 `credentials.existingSecret`。Chart 不接收、不渲染真实密钥。Secret 的 `api_token` 用于保护 `/api/v1/*`；客户端发送 `Authorization: Bearer <token>`。短期 token 对应 `credentials.optionalItems`，不使用的 Provider 和 Secret key 应从 values 中一并删除。

资源到集群的映射通过 `resourceMappings` 显式配置。现有 Dashboard 兼容费率通过 `nodeRates` 配置，指标包含 Provider、账号、集群、区域、实例类型和币种。更通用的费率目录通过 Provider 的 `options.rate_card_json` 注入。

## 6. API 与指标口径

| 路径 | 语义 |
| --- | --- |
| `GET /healthz` | 进程健康，不依赖云 API 成功 |
| `GET /readyz` | 至少一个 Provider 完成过一次账期采集 |
| `GET /api/v1/providers` | Provider 能力、最后成功时间和独立错误状态 |
| `GET /api/v1/costs` | 可分页、按云/账号/集群/账期/资源/类别过滤的真实账单行 |
| `GET /api/v1/costs/summary` | 精确十进制真实账单汇总 |
| `GET /api/v1/rates` | 按云、账号、区域、资源类型、实例类型和付费模式查询配置费率，返回 `estimated_rate` |
| `POST /api/v1/allocate` | 对单条真实账单执行守恒分摊 |
| `GET /metrics` | 采集健康、台账汇总和低基数展示指标 |

`/api/v1/*` 默认使用可轮换 Bearer Token，服务每次请求重新读取 token 文件；探针和 Prometheus 路径不要求该 token。真实账单查询响应明确带 `costType: "actual_bill"`。`crane_cost_bill_amount{cost_type="list|net|amortized"}` 是账单汇总；`node_total_hourly_cost`、`node_cpu_hourly_cost`、`node_ram_hourly_cost` 是配置费率形成的**估算指标**。Dashboard 标题和说明已改为“预估成本”，避免与实付混用。

## 7. Kubernetes 现代化内容

- Go 基线升级到 1.25；Kubernetes 核心模块统一到 1.35.6，controller-runtime 升级到 0.23.3，cAdvisor 升级到 0.53.0。
- 所有实际 HPA 读写迁移到 `autoscaling/v2`；Pod 驱逐迁移到 `policy/v1`；CRI 客户端迁移到 `runtime/v1` 和独立 `k8s.io/cri-client`。
- CPUSet 改用稳定的 `k8s.io/utils/cpuset`，controller-runtime manager、缓存、webhook、事件处理器和 REST mapper API 已适配新版。
- crane-agent 的 `--cgroup-driver` 默认改为 `auto`：优先读取 `/var/lib/kubelet/config.yaml`，否则在 cgroup v2 主机选择 systemd，在旧层级回退 cgroupfs；仍可显式覆盖。
- `deploy/manifests_1.13` 已标为历史材料，不进入现代安装和 CI。
- 通用镜像构建升级至 Go 1.25/Alpine 3.22，并补充 HTTPS 根证书。

上游 `github.com/gocrane/api v0.12.2` 的 CRD Go 类型仍嵌入 `autoscaling/v2beta2` 结构。当前实现把转换隔离在 `pkg/utils/hpa_conversion.go`，Kubernetes API Server 只接收 `autoscaling/v2` HPA。由于 `k8s.io/api v0.36` 已删除该 Go 包，依赖基线暂选位于目标区间中间的 1.35.6，并由 CI 分别验证 1.34、1.35、1.36 服务端。后续应升级上游 API 仓库并删除这层边界转换。

## 8. 自动验证与支持边界

本地已执行：

```text
go vet ./...
go test ./...
npm run build
helm lint charts/crane-cost-collector
helm template ... --kube-version 1.34.0
helm template ... --kube-version 1.35.0
helm template ... --kube-version 1.36.0
kubectl kustomize deploy/cost-collector
```

本地工作区没有可用 Docker daemon，因此没有在本机实际创建 Kind 集群或构建容器镜像；这些步骤已经固化到兼容性工作流，需以首次 GitHub Actions 运行结果作为容器/Kind 证据，不能把本地 Chart 渲染结果冒充为已完成的集群 E2E。

`.github/workflows/kubernetes-compatibility.yml` 使用 Kind 0.32.0 与固定 digest 的 Kubernetes 1.34.8、1.35.5、1.36.1 节点镜像，执行：

1. Chart lint/render 和目标 API Server dry-run；
2. 当前 CRD 的目标 API Server dry-run；
3. 构建并导入 `cost-collector` 镜像；
4. 安装成本只读模式并探测 `/healthz`；
5. Helm upgrade 和 uninstall。

当前可承诺的支持范围：

| 档位 | 状态 |
| --- | --- |
| Kubernetes 1.34-1.36 成本只读模式 | 已实现并进入 CI |
| 四云账单 API 接口、统一台账、查询和分摊 | 已实现并有契约/单元测试 |
| EHPA/推荐/预测等控制面源码兼容 | 已完成迁移并通过全仓编译测试，尚缺目标集群 E2E |
| crane-agent + containerd + cgroup v2/systemd | 已完成源码迁移和自动探测，尚缺 Linux 节点实机 E2E |
| CRI-O、PDB/优雅驱逐、证书轮换 | 尚缺运行时回归 |
| ACK/VKE/CCE/TKE 与四云财务准确性 | 必须使用真实环境验收后才能承诺 |

## 9. 下一轮验收输入

要从“接口级支持”升级为“生产支持”，需要提供：

1. 四家云各一个只读测试账号、一个月脱敏账单和对应节点清单；
2. ACK、VKE、CCE、TKE 各一套非生产集群及其 Kubernetes/OS/containerd 版本；
3. 企业财务口径：退款、调账、主子账号、摊销、币种和允许差异门槛；
4. 预期数据量、历史保留期和目标查询并发，用于选择 PostgreSQL 或 ClickHouse；
5. 如需完整 Crane 节点能力，至少一套 CRI-O 和节点重启/运行时重启测试面。
