# 多云成本只读部署

此目录只部署 `cost-collector`，不启用 crane-agent、EHPA、QoS 驱逐或 Metric Adapter，适合作为新版 Kubernetes 集群的第一阶段成本接入。

如需可配置安装，使用 `charts/crane-cost-collector`；Chart 明确约束 Kubernetes 1.34-1.36、单副本文件台账和持久化存储。CI 会在三个版本的 Kind API Server 上验证模板/CRD，并实际安装、启动和探测成本只读服务。

## 部署前

1. 修改 `configmap.yaml` 中四家云的付款账号、区域、资源映射和节点费率。`namespace.yaml` 会创建独立的 `crane-cost-system` 并启用 Restricted Pod Security，不改变已有 `crane-system`。
2. 使用外部 Secret 管理系统创建 `crane-cost-credentials`。`secret.example.yaml` 仅列出键名，禁止提交真实凭证。
3. 把 Deployment 镜像替换为本次源码构建的不可变 tag 或 digest。
4. 云账号应只授予账单明细只读权限。华为云 `auth_token` 等短期凭证可由 sidecar/token agent 更新 Secret 投射文件；采集器每次请求都会重新读取。

```bash
kubectl apply -f deploy/cost-collector/namespace.yaml
kubectl apply -f secret.yaml
kubectl apply -k deploy/cost-collector
kubectl -n crane-cost-system port-forward svc/crane-cost-collector 8080:8080
curl http://127.0.0.1:8080/healthz
curl -H "Authorization: Bearer $CRANE_COST_API_TOKEN" http://127.0.0.1:8080/api/v1/providers
curl -H "Authorization: Bearer $CRANE_COST_API_TOKEN" 'http://127.0.0.1:8080/api/v1/costs/summary?billing_period=2026-07'
curl -H "Authorization: Bearer $CRANE_COST_API_TOKEN" 'http://127.0.0.1:8080/api/v1/rates?provider=aliyun&account_id=PAYER&resource_type=ecs&region=cn-hangzhou'
```

Prometheus 抓取 `/metrics`。配置 `nodeRates` 后会输出兼容现有 Crane Dashboard 的 `node_total_hourly_cost`、`node_cpu_hourly_cost`、`node_ram_hourly_cost`；账单金额另以 `crane_cost_bill_amount` 输出，明细资源 ID 不进入指标标签。

`/api/v1/rates` 仅查询 Provider `options.rate_card_json` 中经过确认的合同价/自定义费率；未配置时返回 404，不会用虚构默认值代替云厂商报价。

当前文件台账面向单副本只读版，Deployment 因此使用 `Recreate`。大规模明细或多副本生产部署应实现同一 `ledger.Store` 接口的 PostgreSQL/ClickHouse 后端，并把云厂商原始账单文件另行归档。

`/api/v1/*` 默认从 Secret 投射文件读取轮换 Bearer Token；`/healthz`、`/readyz` 和 `/metrics` 保留给 Kubernetes 探针与 Prometheus。NetworkPolicy 仅开放 HTTP 服务端口，出站仅开放 DNS 和 HTTPS。
