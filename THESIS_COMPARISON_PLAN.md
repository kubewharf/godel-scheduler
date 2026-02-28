# 研究生论文 — 分布式调度器性能对比实验方案

## 1. 实验总览

### 1.1 研究目标

验证**独立 Binder（Embedded Binder）架构**相较于**共享 Binder（Shared Binder）架构**在调度吞吐量、绑定延迟、容错能力、资源利用率、Pod 分布均衡度、稳定性与扩展性方面的系统性提升。

### 1.2 对比组设计

| 标识 | 配置 | 说明 |
|------|------|------|
| **A — 共享 Binder（Baseline）** | `--enable-embedded-binder=false` + 独立 Binder Deployment（replicas=1） | 原始 Gödel Scheduler 架构，所有 Scheduler 共用一个 Binder |
| **B — 独立 Binder（Proposed）** | `--enable-embedded-binder=true` + Binder Deployment replicas=0 | 论文提出的架构，每个 Scheduler 内嵌独立 Binder |
| **C — kube-scheduler（Reference）** | 原生 Kubernetes 调度器（单实例） | 行业基准参考，用于凸显分布式架构的整体优势 |

> **所有实验**至少执行 **3 次**，取**中位数 + 标准差**，确保统计显著性。

### 1.3 部署命令

```bash
# 组 A：共享 Binder（原始架构）
kubectl apply -k manifests/base/

# 组 B：独立 Binder（论文架构）
kubectl apply -k manifests/overlays/embedded-binder/

# 组 C：kube-scheduler
# 禁用 Gödel，启用原生 kube-scheduler
```

---

## 2. 实验环境

### 2.1 硬件配置

| 项目 | 规格 |
|------|------|
| 物理机 | 1 台 |
| CPU | Apple M3 Max（或 Intel Xeon Platinum 8260 48C/96T） |
| 内存 | ≥ 64 GB |
| 存储 | NVMe SSD |
| 网络 | 千兆以太网（物理集群）/ loopback（kind 集群） |

### 2.2 软件环境

| 项目 | 版本 |
|------|------|
| Kubernetes | 1.29.x |
| Gödel Scheduler | 当前 commit（含全部 7 个 Phase 改造） |
| KWOK | 最新稳定版（模拟大规模节点） |
| Prometheus | v2.51.0（已部署，NodePort 30090） |
| kind | 最新版（本地集群） |
| Go | 1.21+ |

### 2.3 集群规模梯度

| 规模编号 | 节点数 | Scheduler 实例数 | 说明 |
|----------|--------|------------------|------|
| S1 | 100 | 1 | 小规模基线 |
| S2 | 1,000 | 3 | 中等规模 |
| S3 | 5,000 | 3 | 大规模 |
| S4 | 10,000 | 3 | 超大规模 |
| S5 | 30,000 | 3 | 极限规模（参照官方 best-practice） |

> 使用 KWOK 模拟节点：`seq $N | xargs -I {} -P 50 bash -c "kubectl create -f docs/performance/node.yaml"`

---

## 3. 负载模型

### 3.1 Pod 模板

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: bench-pod-${INDEX}
  namespace: bench
  annotations:
    godel.bytedance.com/pod-state: pending
    godel.bytedance.com/pod-resource-type: guaranteed
    godel.bytedance.com/pod-launcher: kubelet
spec:
  schedulerName: godel-scheduler
  containers:
    - name: app
      image: pause:3.9
      resources:
        requests:
          cpu: "${CPU}m"
          memory: "${MEM}Mi"
```

### 3.2 负载场景矩阵

| 场景 ID | 场景名称 | Pod 创建速率 | 总 Pod 数 | Pod 资源规格 | PodGroup | 说明 |
|---------|----------|-------------|-----------|-------------|----------|------|
| W1 | 低负载稳态 | 100 pods/s | 10,000 | cpu:100m, mem:128Mi | 无 | 基线性能 |
| W2 | 中负载稳态 | 500 pods/s | 50,000 | cpu:100m, mem:128Mi | 无 | 常规工作负载 |
| W3 | 高负载稳态 | 1,000 pods/s | 100,000 | cpu:100m, mem:128Mi | 无 | 高压场景 |
| W4 | 极限负载 | 2,000 pods/s | 200,000 | cpu:100m, mem:128Mi | 无 | 压力极限 |
| W5 | 突发洪峰 | 0→2000→0 pods/s | 50,000 | cpu:100m, mem:128Mi | 无 | 阶梯式突发 |
| W6 | Gang 调度 | 200 groups/s | 10,000 pods | cpu:100m, mem:128Mi | 5 pods/group | 批量调度场景 |
| W7 | 异构资源 | 500 pods/s | 50,000 | 混合（见下） | 无 | 资源碎片化测试 |
| W8 | 大规模集群 | 2,000 pods/s | 800,000 | cpu:100m, mem:128Mi | 无 | 参照官方 best-practice |

**W7 异构资源混合比例：**
- 30% 小规格：cpu:50m, mem:64Mi
- 40% 中规格：cpu:200m, mem:256Mi
- 20% 大规格：cpu:1000m, mem:1Gi
- 10% 超大规格：cpu:4000m, mem:8Gi

### 3.3 Pod 创建工具

使用批量 Pod 创建脚本（参考 `docs/performance/best-practice.md`），确保稳定的创建速率：

```bash
#!/bin/bash
# bench-create-pods.sh
RATE=${1:-500}        # pods/s
TOTAL=${2:-50000}     # total pods
NAMESPACE="bench"
TEMPLATE="bench-pod-template.yaml"

kubectl create namespace $NAMESPACE --dry-run=client -o yaml | kubectl apply -f -

for i in $(seq 1 $TOTAL); do
  sed "s/\${INDEX}/$i/g; s/\${CPU}/100/g; s/\${MEM}/128/g" $TEMPLATE | \
    kubectl apply -f - &
  if (( i % RATE == 0 )); then
    sleep 1
  fi
done
wait
```

---

## 4. 指标体系与采集方案

### 4.1 指标总表

以下所有指标均通过已部署的 Prometheus（NodePort 30090）采集，采集间隔 15s。

---

### 4.2 维度一：吞吐量 (Throughput)

**论文意义**：直接衡量系统处理能力，是分布式调度器最核心的性能指标。

| 指标 | Prometheus 查询 | 单位 | 目标 |
|------|----------------|------|------|
| 调度吞吐量（E2E） | `sum(rate(scheduler_pod_scheduling_attempts{result="scheduled"}[1m]))` | pods/s | B > A |
| 绑定吞吐量（Pod 级） | `sum(godel:binder_embedded_bind_pods:rate1m{result="success"})` | pods/s | B > A |
| 绑定吞吐量（Unit 级） | `sum(godel:binder_embedded_bind_units:rate1m{result="success"})` | units/s | B > A |
| 峰值吞吐量 | `max_over_time(sum(rate(scheduler_pod_scheduling_attempts{result="scheduled"}[1m]))[30m:])` | pods/s | B > A |
| 持续吞吐量 | 稳态期间的平均值 | pods/s | B > A |

**图表设计**：

| 图表编号 | 图表类型 | X 轴 | Y 轴 | 系列 |
|----------|----------|------|------|------|
| T-1 | 折线图 | 时间 (s) | 调度吞吐量 (pods/s) | A / B / C |
| T-2 | 柱状图 | 集群规模 (S1–S5) | 峰值吞吐量 (pods/s) | A / B / C |
| T-3 | 柱状图 | 负载场景 (W1–W4) | 平均吞吐量 (pods/s) | A / B |
| T-4 | 折线图 | Scheduler 实例数 (1,2,3,5) | 吞吐量 (pods/s) | A / B（扩展性） |

---

### 4.3 维度二：延迟 (Latency)

**论文意义**：衡量单个 Pod 从提交到完成调度/绑定的响应时间，反映实时性。

| 指标 | Prometheus 查询 | 单位 | 目标 |
|------|----------------|------|------|
| E2E 调度延迟 P50 | `godel:scheduler_e2e_scheduling_duration:p50` | s | B ≤ A |
| E2E 调度延迟 P90 | `godel:scheduler_e2e_scheduling_duration:p90` | s | B < A |
| E2E 调度延迟 P99 | `godel:scheduler_e2e_scheduling_duration:p99` | s | B < A |
| E2E 调度延迟 AVG | `godel:scheduler_e2e_scheduling_duration:avg` | s | B < A |
| 绑定延迟 P50（Pod 级） | `godel:binder_embedded_bind_pod_duration:p50` | s | B < A |
| 绑定延迟 P90（Pod 级） | `godel:binder_embedded_bind_pod_duration:p90` | s | B < A |
| 绑定延迟 P99（Pod 级） | `godel:binder_embedded_bind_pod_duration:p99` | s | B < A |
| 绑定延迟 AVG（Unit 级） | `godel:binder_embedded_bind_duration:avg` | s | B < A |
| 调度+绑定组合 P90 | `godel:e2e_schedule_and_bind_duration:p90_estimate` | s | B < A |
| 调度+绑定组合 P99 | `godel:e2e_schedule_and_bind_duration:p99_estimate` | s | B < A |
| 核心算法延迟 P90 | `godel:scheduler_scheduling_algorithm_duration:p90` | s | B ≈ A（无退化） |
| 核心算法延迟 P99 | `godel:scheduler_scheduling_algorithm_duration:p99` | s | B ≈ A（无退化） |
| 队列等待时间 | `histogram_quantile(0.90, rate(scheduler_pod_pending_in_queue_duration_seconds_bucket[5m]))` | s | B < A |

**图表设计**：

| 图表编号 | 图表类型 | X 轴 | Y 轴 | 系列 |
|----------|----------|------|------|------|
| L-1 | 折线图 | 时间 (s) | E2E 调度延迟 P99 (ms) | A / B / C |
| L-2 | 柱状分组图 | 百分位数 (P50/P90/P99) | 延迟 (ms) | A / B（绑定延迟） |
| L-3 | 箱线图 | 配置组 (A/B) | E2E 延迟 (ms) | 分布对比 |
| L-4 | 折线图 | Pod 创建速率 (100–2000 pods/s) | P99 延迟 (ms) | A / B（延迟 vs 负载） |
| L-5 | CDF 曲线 | 延迟 (ms) | 累积概率 | A / B |
| L-6 | 堆积柱状图 | 配置组 (A/B) | 调度延迟 + 绑定延迟 (ms) | Pipeline 分解 |

---

### 4.4 维度三：容错率 (Fault Tolerance)

**论文意义**：验证独立 Binder 的故障隔离能力，证明架构在故障场景下的鲁棒性。

#### 4.4.1 故障注入场景

| 故障编号 | 故障类型 | 注入方式 | 预期差异 |
|----------|----------|----------|----------|
| F1 | Binder 进程崩溃 | `kubectl delete pod <binder-pod>` | A：全局调度停止；B：仅影响一个 Scheduler |
| F2 | 单个 Scheduler 崩溃 | `kubectl delete pod <scheduler-pod>` | A：Binder 仍工作；B：该 Scheduler 的 Binder 同时停止但其他正常 |
| F3 | 节点重新分区 | 触发 Node Shuffler 重新平衡 | B：NodeValidator 拦截过期绑定 |
| F4 | API Server 短暂不可用 | `kubectl cordon` + 短暂网络隔离（3s） | B：指数退避重试 + 不丢失请求 |
| F5 | API Server 高延迟 | 注入 100ms 网络延迟 | B：重试计数增加但不崩溃 |

#### 4.4.2 容错指标

| 指标 | 采集方式 | 单位 | 目标 |
|------|----------|------|------|
| 故障恢复时间 (MTTR) | `timestamp(binder_embedded_bind_inflight > 0) - timestamp(故障发生)` | s | B < A |
| 故障期间调度中断时长 | 观测 `rate(scheduler_pod_scheduling_attempts[1m]) == 0` 的持续时间 | s | B < A |
| 故障期间丢失绑定请求数 | `binder_embedded_bind_total{result="failure"}` 在故障窗口内的增量 | 个 | B < A |
| Dispatcher 回退次数 | `godel:binder_dispatcher_fallback:rate5m` | pods/s | B 有此机制，A 无 |
| 节点验证拦截次数 | `godel:binder_node_validation_failures:rate5m` | 次/s | 仅 B 有 |
| 并发 inflight 绑定 | `binder_embedded_bind_inflight` | 个 | 故障时 B 仅影响单实例 |

**图表设计**：

| 图表编号 | 图表类型 | X 轴 | Y 轴 | 系列 |
|----------|----------|------|------|------|
| F-1 | 折线图 | 时间 (s)（故障注入点标记竖线） | 吞吐量 (pods/s) | A / B |
| F-2 | 柱状图 | 故障场景 (F1–F5) | 恢复时间 (s) | A / B |
| F-3 | 热力图 | 时间 × Scheduler 实例 | 绑定状态（正常/故障） | A / B |

---

### 4.5 维度四：调度成功率

**论文意义**：衡量系统在高负载下的可靠性，失败率直接影响业务 SLA。

| 指标 | Prometheus 查询 | 单位 | 目标 |
|------|----------------|------|------|
| 调度成功率 | `sum(rate(scheduler_pod_scheduling_attempts{result="scheduled"}[5m])) / sum(rate(scheduler_pod_scheduling_attempts[5m]))` | % | B ≥ A |
| 调度失败率 | `sum(rate(scheduler_pod_scheduling_attempts{result="error"}[5m])) / sum(rate(scheduler_pod_scheduling_attempts[5m]))` | % | B ≤ A |
| Unschedulable 比例 | `sum(rate(scheduler_pod_scheduling_attempts{result="unschedulable"}[5m])) / sum(rate(scheduler_pod_scheduling_attempts[5m]))` | % | B ≤ A |
| 绑定成功率（Pod 级） | `godel:binder_embedded_bind_pods:success_rate5m` | % | B > A |
| 绑定成功率（Unit 级） | `godel:binder_embedded_bind_units:success_rate5m` | % | B > A |
| API 重试速率 | `godel:binder_embedded_bind_retries:rate5m` | 次/s | B < A |
| Dispatcher 回退速率 | `godel:binder_dispatcher_fallback:rate5m` | pods/s | 仅 B 有 |
| Pending Pod 数量 | `sum(scheduler_pending_pods)` | 个 | B < A（队列更短） |
| Pending Pod 滞留时间 | `histogram_quantile(0.90, rate(scheduler_pod_pending_in_queue_duration_seconds_bucket[5m]))` | s | B < A |

**图表设计**：

| 图表编号 | 图表类型 | X 轴 | Y 轴 | 系列 |
|----------|----------|------|------|------|
| S-1 | 折线图 | 时间 (s) | 调度成功率 (%) | A / B |
| S-2 | 柱状分组图 | 负载场景 | 成功率 / 失败率 / Unschedulable | A / B |
| S-3 | 折线图 | 时间 (s) | Pending Pod 数量 | A / B |
| S-4 | 柱状图 | 配置组 | 平均重试次数 | A / B |

---

### 4.6 维度五：资源利用率 — 集群均衡利用

**论文意义**：独立 Binder 的低延迟使调度决策更及时，理论上可提升集群资源利用率的均衡度。

#### 4.6.1 采集方式

- **KWOK 模拟节点**：KWOK 节点无真实资源使用，通过 Scheduler Cache 中的 `Requested` vs `Allocatable` 计算
- **真实 kind 节点**：通过 `metrics-server` 或 `node_exporter` + Prometheus 采集

#### 4.6.2 指标

| 指标 | 计算方式 | 单位 | 目标 |
|------|----------|------|------|
| 节点 CPU 利用率 | `sum(kube_pod_container_resource_requests{resource="cpu"}) by (node) / kube_node_status_allocatable{resource="cpu"}` | % | — |
| 节点 Memory 利用率 | `sum(kube_pod_container_resource_requests{resource="memory"}) by (node) / kube_node_status_allocatable{resource="memory"}` | % | — |
| 平均 CPU 利用率 | `avg(上述)` | % | B ≥ A |
| CPU 利用率方差 | `stddev(节点CPU利用率)` | — | B < A（更均衡） |
| CPU 利用率变异系数 (CV) | `stddev / avg` | — | B < A |
| Memory 利用率方差 | `stddev(节点Memory利用率)` | — | B < A |
| 节点利用率基尼系数 | 离线计算 | 0–1 | B < A（0=完全均衡） |

> **注意**：KWOK 节点无真实资源消耗。资源利用率通过 `Requested / Allocatable` 计算（代表"调度层面的利用率"），这在调度器对比中是合理且常用的。

#### 4.6.3 数据采集脚本

```bash
#!/bin/bash
# collect-node-utilization.sh
# 从 Scheduler Cache 指标中提取节点资源利用率

echo "node,cpu_requested,cpu_allocatable,mem_requested,mem_allocatable" > utilization.csv

# 通过 Prometheus API 查询
CPU_REQ=$(curl -s "http://localhost:30090/api/v1/query?query=scheduler_cluster_pod_requested{resource='cpu'}" | jq -r '.data.result')
CPU_ALLOC=$(curl -s "http://localhost:30090/api/v1/query?query=scheduler_cluster_allocatable{resource='cpu'}" | jq -r '.data.result')

# ... 解析并写入 CSV
```

**图表设计**：

| 图表编号 | 图表类型 | X 轴 | Y 轴 | 系列 |
|----------|----------|------|------|------|
| U-1 | 箱线图 | 配置组 (A/B) | 节点 CPU 利用率 (%) | 分布对比 |
| U-2 | 折线图 | 时间 (s) | CPU 利用率方差 | A / B |
| U-3 | 热力图 | 节点 ID × 时间 | CPU 利用率 (%) | A vs B 对比 |
| U-4 | 柱状图 | 配置组 | 变异系数 (CV) | A / B |

---

### 4.7 维度六：资源碎片化

**论文意义**：验证更快的绑定速度是否能减少调度"间隙"造成的资源碎片。

| 指标 | 计算方式 | 单位 | 目标 |
|------|----------|------|------|
| 可分配资源 vs 实际使用 | `sum(Allocatable) - sum(Requested)` | CPU cores / GiB | — |
| 资源浪费比例 | `(sum(Allocatable) - sum(Requested)) / sum(Allocatable)` | % | B ≤ A |
| 碎片化指数 | `count(node: Requested/Allocatable < 0.5 && Requested > 0) / count(all_nodes)` | % | B < A |
| 不可用资源占比 | `sum(node: free_cpu < min_pod_request) / sum(Allocatable)` | % | B < A |
| 最大可调度 Pod 数 | 在所有 Pod 调度完后，统计仍能放置标准 Pod 的数量 | 个 | B ≥ A |

**计算方式**：

```python
# fragmentation_analysis.py
import pandas as pd
import numpy as np

def compute_fragmentation(utilization_csv):
    df = pd.read_csv(utilization_csv)
    df['cpu_util'] = df['cpu_requested'] / df['cpu_allocatable']
    df['mem_util'] = df['mem_requested'] / df['mem_allocatable']
    
    # 碎片化指数：有使用但利用率 < 50% 的节点比例
    fragmented = df[(df['cpu_util'] > 0) & (df['cpu_util'] < 0.5)]
    frag_index = len(fragmented) / len(df)
    
    # 资源浪费比例
    total_alloc = df['cpu_allocatable'].sum()
    total_req = df['cpu_requested'].sum()
    waste_ratio = (total_alloc - total_req) / total_alloc
    
    # 基尼系数
    util_values = df['cpu_util'].values
    gini = compute_gini(util_values)
    
    return {
        'fragmentation_index': frag_index,
        'waste_ratio': waste_ratio,
        'gini_coefficient': gini,
        'cv': np.std(util_values) / np.mean(util_values)
    }
```

**图表设计**：

| 图表编号 | 图表类型 | X 轴 | Y 轴 | 系列 |
|----------|----------|------|------|------|
| R-1 | 堆积柱状图 | 配置组 (A/B) | 资源量 (CPU cores) | 已用 / 碎片 / 空闲 |
| R-2 | 柱状图 | 负载场景 | 碎片化指数 (%) | A / B |
| R-3 | 散点图 | CPU 利用率 (%) | Memory 利用率 (%) | 节点分布（A vs B） |

---

### 4.8 维度七：Pod 分布均衡度

**论文意义**：验证独立 Binder 是否影响 Dispatcher 的节点分区均衡效果。

| 指标 | 计算方式 | 单位 | 目标 |
|------|----------|------|------|
| 每节点 Pod 数方差 | `stddev(count(pods) group by node)` | — | B ≤ A |
| 每节点 Pod 数变异系数 | `stddev / avg` | — | B ≤ A |
| 每 Scheduler 分区 Pod 数 | `count(pods) group by scheduler` | 个 | 各组均匀 |
| Pod/节点比例最大偏差 | `max(node_pod_count) / avg(node_pod_count) - 1` | — | B ≤ A |
| Jain 公平性指数 | $J(x_1,...,x_n) = \frac{(\sum x_i)^2}{n \cdot \sum x_i^2}$ | 0–1 | B ≥ A |

**数据采集**：

```bash
# 采集每节点 Pod 数
kubectl get pods -A -o json | jq -r '.items[] | select(.spec.nodeName != null) | .spec.nodeName' | sort | uniq -c | sort -n > pod-distribution.txt

# 采集每 Scheduler 分区的 Pod 数
for sched in scheduler-0 scheduler-1 scheduler-2; do
  echo "$sched: $(kubectl get pods -A -o json | jq "[.items[] | select(.metadata.annotations[\"godel.bytedance.com/selected-scheduler\"]==\"$sched\")] | length")"
done
```

**图表设计**：

| 图表编号 | 图表类型 | X 轴 | Y 轴 | 系列 |
|----------|----------|------|------|------|
| D-1 | 箱线图 | 配置组 (A/B) | 每节点 Pod 数 | 分布对比 |
| D-2 | 柱状分组图 | Scheduler 实例 | Pod 数 | A / B |
| D-3 | 直方图 | 每节点 Pod 数 | 节点数 | A / B 叠加 |

---

### 4.9 维度八：稳定性 & 抗压能力

**论文意义**：证明系统在持续高负载下不退化，不存在性能悬崖。

#### 4.9.1 实验设计

| 测试编号 | 测试名称 | 方法 | 持续时间 |
|----------|----------|------|----------|
| ST-1 | 持续高负载 | 以 W3（1000 pods/s）持续运行 | 30 min |
| ST-2 | 阶梯式增压 | 每 5 min 增 200 pods/s（100→2000） | 50 min |
| ST-3 | 突发洪峰 | 平稳 200 pods/s → 突发 2000 pods/s × 60s → 回落 | 10 min |
| ST-4 | 长时间运行 | W2（500 pods/s）持续运行 | 120 min |

#### 4.9.2 指标

| 指标 | 采集方式 | 单位 | 目标 |
|------|----------|------|------|
| P99 延迟随时间趋势 | 每分钟采样 P99 | ms | B：稳定；A：可能上升 |
| 吞吐量随时间趋势 | 每分钟采样 throughput | pods/s | B：稳定；A：可能下降 |
| 延迟是否线性增长 | 线性回归延迟趋势斜率 | ms/min | B ≈ 0；A > 0 |
| Scheduler 阻塞检测 | `scheduler_goroutines` 是否异常增长 | 个 | B < A |
| Pending Pod 堆积 | `max(scheduler_pending_pods)` | 个 | B < A |
| inflight 绑定峰值 | `max(binder_embedded_bind_inflight)` | 个 | B 可控 |
| GC 暂停时间 | Go runtime metrics（如有） | ms | B ≤ A |

**图表设计**：

| 图表编号 | 图表类型 | X 轴 | Y 轴 | 系列 |
|----------|----------|------|------|------|
| ST-1 | 折线图 | 时间 (min) | P99 延迟 (ms) | A / B（30min 持续） |
| ST-2 | 双轴折线图 | 时间 (min) | 左：吞吐量；右：P99 延迟 | A / B（阶梯增压） |
| ST-3 | 折线图 | 时间 (s) | 吞吐量 (pods/s) | A / B（突发洪峰） |
| ST-4 | 折线图（带趋势线） | 时间 (min) | P99 延迟 (ms) | A / B + 线性趋势线 |

---

### 4.10 维度九：扩展性 (Scalability)

**论文意义**：核心论点 — 独立 Binder 使系统能力随 Scheduler 数量线性扩展。

#### 4.10.1 水平扩展（Scheduler 实例数）

| 实验编号 | Scheduler 实例数 | 集群规模 | 负载 |
|----------|-----------------|----------|------|
| SC-1 | 1 | 5,000 nodes | W3 |
| SC-2 | 2 | 5,000 nodes | W3 |
| SC-3 | 3 | 5,000 nodes | W3 |
| SC-4 | 5 | 5,000 nodes | W3 |

**关键观测**：

| 指标 | 计算方式 | 理想值 | 目标 |
|------|----------|--------|------|
| 吞吐量扩展比 | `throughput(N) / throughput(1)` | N | B 接近 N，A < N |
| 延迟扩展比 | `latency(N) / latency(1)` | 1（不增长） | B ≈ 1，A > 1 |
| 扩展效率 | `throughput(N) / (N × throughput(1))` | 1 | B > A |

#### 4.10.2 垂直扩展（集群规模）

| 实验编号 | 节点数 | Scheduler 实例数 | Pod 总数 |
|----------|--------|------------------|----------|
| VS-1 | 100 | 3 | 5,000 |
| VS-2 | 1,000 | 3 | 50,000 |
| VS-3 | 5,000 | 3 | 200,000 |
| VS-4 | 10,000 | 3 | 500,000 |
| VS-5 | 30,000 | 3 | 800,000 |

**图表设计**：

| 图表编号 | 图表类型 | X 轴 | Y 轴 | 系列 |
|----------|----------|------|------|------|
| SC-1 | 折线图 | Scheduler 实例数 (1–5) | 吞吐量 (pods/s) | A / B + 理想线性线 |
| SC-2 | 折线图 | Scheduler 实例数 (1–5) | P99 延迟 (ms) | A / B |
| SC-3 | 柱状图 | Scheduler 实例数 | 扩展效率 (%) | A / B |
| VS-1 | 折线图 | 集群规模 (节点数) | 吞吐量 (pods/s) | A / B / C |
| VS-2 | 折线图 | 集群规模 (节点数) | P99 延迟 (ms) | A / B / C |

---

## 5. 实验执行步骤

### 5.1 完整实验流程

```
Phase 0: 环境准备 (1 天)
├── 搭建 kind 集群 / 物理集群
├── 部署 KWOK 模拟节点
├── 验证 Prometheus 可达
├── 准备 Pod 模板和创建脚本
└── 编写数据采集自动化脚本

Phase 1: 基线实验 — 组 A（共享 Binder）(2 天)
├── 部署组 A 配置
├── 依次执行 W1–W8 负载
├── 每个场景采集全量指标
├── 执行故障注入 F1–F5
├── 执行扩展性测试 SC-1~4, VS-1~5
└── 导出 Prometheus 快照

Phase 2: 对比实验 — 组 B（独立 Binder）(2 天)
├── 切换到组 B 配置
├── 重复 Phase 1 全部实验
├── 额外采集 embedded binder 专有指标
└── 导出 Prometheus 快照

Phase 3: 参考基线 — 组 C（kube-scheduler）(1 天)
├── 禁用 Gödel，启用 kube-scheduler
├── 执行 W1–W4 + VS-1~5
└── 导出 Prometheus 快照

Phase 4: 数据分析与可视化 (2 天)
├── 数据清洗与对齐
├── 生成所有图表
├── 统计显著性检验
└── 撰写论文实验章节
```

### 5.2 单次实验标准操作流程 (SOP)

```bash
#!/bin/bash
# run-experiment.sh <config> <workload> <run_id>
CONFIG=$1    # a|b|c
WORKLOAD=$2  # w1|w2|w3|w4|w5|w6|w7|w8
RUN_ID=$3    # 1|2|3

RESULTS_DIR="results/${CONFIG}/${WORKLOAD}/run${RUN_ID}"
mkdir -p $RESULTS_DIR

# 1. 清理环境
kubectl delete namespace bench --ignore-not-found
sleep 10

# 2. 重置 Prometheus 指标（可选：重启 Prometheus）
# kubectl rollout restart deployment/prometheus -n monitoring

# 3. 等待系统稳定
sleep 30

# 4. 记录实验开始时间
START_TIME=$(date +%s)
echo "start_time=$START_TIME" > $RESULTS_DIR/metadata.txt

# 5. 执行负载
bash bench-create-pods.sh $RATE $TOTAL

# 6. 等待所有 Pod 完成调度
while [[ $(kubectl get pods -n bench --field-selector=status.phase=Pending --no-headers | wc -l) -gt 0 ]]; do
  sleep 5
done

# 7. 记录实验结束时间
END_TIME=$(date +%s)
echo "end_time=$END_TIME" >> $RESULTS_DIR/metadata.txt
echo "duration=$((END_TIME - START_TIME))" >> $RESULTS_DIR/metadata.txt

# 8. 导出 Prometheus 数据
bash export-prometheus-data.sh $START_TIME $END_TIME $RESULTS_DIR

# 9. 采集节点资源分布快照
bash collect-node-utilization.sh > $RESULTS_DIR/utilization.csv

# 10. 采集 Pod 分布快照
bash collect-pod-distribution.sh > $RESULTS_DIR/pod-distribution.csv
```

### 5.3 Prometheus 数据导出脚本

```bash
#!/bin/bash
# export-prometheus-data.sh <start_ts> <end_ts> <output_dir>
START=$1; END=$2; DIR=$3
PROM="http://localhost:30090"
STEP="15s"

QUERIES=(
  # 吞吐量
  'sum(rate(scheduler_pod_scheduling_attempts{result="scheduled"}[1m]))'
  'sum(rate(binder_embedded_bind_pods_total{result="success"}[1m]))'
  # 延迟
  'histogram_quantile(0.50,sum(rate(scheduler_e2e_scheduling_duration_seconds_bucket[1m]))by(le))'
  'histogram_quantile(0.90,sum(rate(scheduler_e2e_scheduling_duration_seconds_bucket[1m]))by(le))'
  'histogram_quantile(0.99,sum(rate(scheduler_e2e_scheduling_duration_seconds_bucket[1m]))by(le))'
  'histogram_quantile(0.50,sum(rate(binder_embedded_bind_pod_duration_seconds_bucket[1m]))by(le))'
  'histogram_quantile(0.90,sum(rate(binder_embedded_bind_pod_duration_seconds_bucket[1m]))by(le))'
  'histogram_quantile(0.99,sum(rate(binder_embedded_bind_pod_duration_seconds_bucket[1m]))by(le))'
  # 成功率
  'sum(rate(scheduler_pod_scheduling_attempts{result="scheduled"}[5m]))/sum(rate(scheduler_pod_scheduling_attempts[5m]))'
  'sum(rate(scheduler_pod_scheduling_attempts{result="error"}[5m]))/sum(rate(scheduler_pod_scheduling_attempts[5m]))'
  # Pending
  'sum(scheduler_pending_pods)'
  # Binder 特有
  'binder_embedded_bind_inflight'
  'sum(rate(binder_embedded_bind_retries_total[5m]))'
  'sum(rate(binder_dispatcher_fallback_total[5m]))'
  # Goroutines
  'sum(scheduler_goroutines) by (work)'
)

NAMES=(
  "scheduling_throughput" "bind_throughput"
  "scheduling_latency_p50" "scheduling_latency_p90" "scheduling_latency_p99"
  "bind_latency_p50" "bind_latency_p90" "bind_latency_p99"
  "scheduling_success_rate" "scheduling_error_rate"
  "pending_pods"
  "bind_inflight" "bind_retries" "dispatcher_fallback"
  "goroutines"
)

for i in "${!QUERIES[@]}"; do
  curl -s "$PROM/api/v1/query_range?query=${QUERIES[$i]}&start=$START&end=$END&step=$STEP" \
    > "$DIR/${NAMES[$i]}.json"
done
```

---

## 6. 论文图表汇总

### 6.1 图表清单（共 28 张）

| 维度 | 编号 | 图表名称 | 类型 |
|------|------|----------|------|
| 吞吐量 | T-1 | 调度吞吐量随时间变化曲线 | 折线图 |
| 吞吐量 | T-2 | 不同集群规模下峰值吞吐量 | 柱状图 |
| 吞吐量 | T-3 | 不同负载场景平均吞吐量 | 柱状图 |
| 吞吐量 | T-4 | 吞吐量随 Scheduler 实例数扩展 | 折线图 |
| 延迟 | L-1 | E2E 调度延迟 P99 时间序列 | 折线图 |
| 延迟 | L-2 | 绑定延迟百分位对比 | 柱状分组图 |
| 延迟 | L-3 | E2E 延迟分布对比 | 箱线图 |
| 延迟 | L-4 | 延迟随负载变化趋势 | 折线图 |
| 延迟 | L-5 | 延迟 CDF 曲线 | CDF 曲线 |
| 延迟 | L-6 | Pipeline 延迟分解 | 堆积柱状图 |
| 容错 | F-1 | 故障注入期间吞吐量变化 | 折线图 |
| 容错 | F-2 | 各故障场景恢复时间 | 柱状图 |
| 容错 | F-3 | 故障影响范围热力图 | 热力图 |
| 成功率 | S-1 | 调度成功率时间序列 | 折线图 |
| 成功率 | S-2 | 各场景成功/失败/Unschedulable | 柱状分组图 |
| 成功率 | S-3 | Pending Pod 堆积曲线 | 折线图 |
| 成功率 | S-4 | 重试次数对比 | 柱状图 |
| 利用率 | U-1 | 节点 CPU 利用率分布 | 箱线图 |
| 利用率 | U-2 | CPU 利用率方差随时间变化 | 折线图 |
| 利用率 | U-3 | 节点利用率热力图 | 热力图 |
| 利用率 | U-4 | 变异系数对比 | 柱状图 |
| 碎片化 | R-1 | 资源分配结构对比 | 堆积柱状图 |
| 碎片化 | R-2 | 碎片化指数对比 | 柱状图 |
| 碎片化 | R-3 | CPU vs Memory 利用率散点 | 散点图 |
| 均衡度 | D-1 | 每节点 Pod 数分布 | 箱线图 |
| 均衡度 | D-2 | 每 Scheduler 分区 Pod 数 | 柱状分组图 |
| 稳定性 | ST-1~4 | 长时间运行稳定性曲线 | 折线图 ×4 |
| 扩展性 | SC-1~3 | 水平扩展性曲线 | 折线图 ×3 |
| 扩展性 | VS-1~2 | 垂直扩展性曲线 | 折线图 ×2 |

---

## 7. 预期结论与论文论证逻辑

### 7.1 各维度预期结论

| 维度 | 预期结论 | 论证依据 |
|------|----------|----------|
| **吞吐量** | B 吞吐量约为 A 的 **1.5–3×**（取决于 Scheduler 实例数） | 消除共享 Binder 的单点队列竞争瓶颈，绑定请求并行处理 |
| **延迟** | B 绑定延迟降低 **50%+**，E2E 延迟降低 **30–50%** | 去除 Scheduler→Binder 的网络开销和队列等待时间 |
| **容错** | B 的 MTTR 降低至 **秒级**，A 为**分钟级** | Binder 故障仅影响单个 Scheduler 分区，其他分区不受影响 |
| **成功率** | B 的调度成功率在高负载下比 A 高 **2–5%** | 绑定更快完成 → Cache 更快更新 → 减少调度冲突 |
| **利用率** | B 的资源利用率 CV 比 A 降低 **10–20%** | 更及时的绑定使全局视图更准确，减少调度偏差 |
| **碎片化** | B 的碎片化指数与 A **持平或略优** | 调度算法不变，但绑定更快减少"临时碎片" |
| **均衡度** | B 与 A **持平**（Dispatcher 负载均衡机制不变） | 独立 Binder 不影响 Dispatcher 的 Pod 分发策略 |
| **稳定性** | B 的 P99 延迟在 30min 内**无上升趋势**，A 可能有 | 无队列积压，无 Binder 端的 back-pressure |
| **扩展性** | B 的吞吐量扩展效率 > **85%**（接近线性），A < **60%** | 独立 Binder 消除共享资源竞争，实现真正的水平扩展 |

### 7.2 论文章节映射

```
第 4 章 系统设计与实现
├── 4.1 共享 Binder 架构问题分析              → 架构对比图 (BINDER_ARCHITECTURE_REFACTORING.md §2)
├── 4.2 独立 Binder 架构设计                  → 设计文档 Phase 1–4
├── 4.3 容错机制（NodeValidator + Retry + Fallback） → Phase 4
└── 4.4 可观测性设计                          → Phase 6–7

第 5 章 实验评估
├── 5.1 实验环境与方法                        → 本文档 §2–3
├── 5.2 吞吐量评估                            → 图表 T-1~T-4
├── 5.3 延迟评估                              → 图表 L-1~L-6
├── 5.4 容错能力评估                          → 图表 F-1~F-3
├── 5.5 调度可靠性评估                        → 图表 S-1~S-4
├── 5.6 资源效率评估                          → 图表 U-1~U-4, R-1~R-3, D-1~D-2
├── 5.7 稳定性与抗压评估                      → 图表 ST-1~ST-4
├── 5.8 扩展性评估                            → 图表 SC-1~SC-3, VS-1~VS-2
└── 5.9 实验结论                              → §7.1 汇总表
```

---

## 8. 数据可视化工具建议

| 工具 | 用途 | 适用图表 |
|------|------|----------|
| **Grafana** | 实时监控仪表盘 + 截图 | T-1, L-1, S-1, ST-1~4 |
| **Python matplotlib/seaborn** | 论文级出版质量图表 | 所有图表 |
| **Prometheus HTTP API** | 数据导出 | 全部时间序列数据 |
| **pandas + numpy** | 数据分析（方差、基尼系数、CV） | U-*, R-*, D-* |
| **scipy.stats** | 统计显著性检验（t-test, Mann-Whitney U） | 全维度 |

### 8.1 Python 绘图框架

```python
# thesis_plots.py — 论文图表统一风格
import matplotlib.pyplot as plt
import matplotlib
import seaborn as sns

# 论文级图表设置
matplotlib.rcParams.update({
    'font.family': 'serif',
    'font.serif': ['Times New Roman'],
    'font.size': 12,
    'axes.labelsize': 14,
    'axes.titlesize': 14,
    'legend.fontsize': 11,
    'xtick.labelsize': 12,
    'ytick.labelsize': 12,
    'figure.figsize': (8, 5),
    'figure.dpi': 300,
    'savefig.dpi': 300,
    'savefig.bbox': 'tight',
})

COLORS = {
    'A (Shared Binder)': '#E74C3C',
    'B (Embedded Binder)': '#2ECC71',
    'C (kube-scheduler)': '#3498DB',
}

def plot_throughput_timeseries(data_a, data_b, data_c=None, output='T-1.pdf'):
    """图表 T-1: 调度吞吐量随时间变化"""
    fig, ax = plt.subplots()
    ax.plot(data_a['time'], data_a['throughput'], label='A (Shared Binder)', color=COLORS['A (Shared Binder)'])
    ax.plot(data_b['time'], data_b['throughput'], label='B (Embedded Binder)', color=COLORS['B (Embedded Binder)'])
    if data_c is not None:
        ax.plot(data_c['time'], data_c['throughput'], label='C (kube-scheduler)', color=COLORS['C (kube-scheduler)'], linestyle='--')
    ax.set_xlabel('Time (s)')
    ax.set_ylabel('Scheduling Throughput (pods/s)')
    ax.legend()
    ax.grid(True, alpha=0.3)
    fig.savefig(output)
    plt.close()
```

---

## 9. 实验注意事项

### 9.1 实验控制变量

| 变量 | 保持一致 |
|------|----------|
| Kubernetes 版本 | 所有组使用同一版本 |
| API Server 配置 | `--max-mutating-requests-inflight=10000 --max-requests-inflight=20000` |
| 节点规格 | KWOK 模拟节点使用相同模板（CPU: 32, Memory: 64Gi） |
| Pod 模板 | 相同的 annotations 和 resource requests |
| 网络条件 | 相同网络环境 |
| 系统负载 | 实验前清理其他进程 |
| Pod 创建速率 | 使用相同工具和参数 |
| 日志级别 | 统一设置为 4 |
| QPS/Burst | 统一设置为 10000 |

### 9.2 常见陷阱

1. **KWOK 节点的资源利用率**：KWOK 节点无真实资源消耗，"利用率"基于 `Requested/Allocatable` 计算，需在论文中说明
2. **API Server 瓶颈**：高负载实验中 API Server 可能成为瓶颈，需确保其有充足资源
3. **Prometheus 采集间隔**：15s 间隔在突发场景下可能丢失瞬态变化，可临时降至 5s
4. **kind 集群限制**：kind 集群性能受宿主机限制，超大规模测试建议使用物理集群
5. **冷启动效应**：每次实验前等待 30s 让系统稳定，正式数据排除前 60s
6. **GC 干扰**：Go 的 GC 可能导致延迟尖刺，多次测量取中位数

### 9.3 统计显著性

每个实验重复 **3–5 次**，报告：
- **中位数**（主要值）
- **标准差**（误差线）
- **Mann-Whitney U 检验**（p < 0.05 为显著）
- **效应量**（Cohen's d 或改善百分比）

---

## 10. 时间规划

| 阶段 | 工作内容 | 预计时间 |
|------|----------|----------|
| 环境准备 | 集群搭建、脚本编写、验证 | 1 天 |
| 组 A 实验 | 全部场景 × 3 次重复 | 2 天 |
| 组 B 实验 | 全部场景 × 3 次重复 | 2 天 |
| 组 C 实验 | 核心场景 × 3 次重复 | 1 天 |
| 数据分析 | 清洗、统计、可视化 | 2 天 |
| 论文撰写 | 实验章节（含图表） | 2 天 |
| **总计** | | **~10 天** |
