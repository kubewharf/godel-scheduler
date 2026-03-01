---
description: 当涉及论文实验方案、性能对比测试、Prometheus 指标采集、实验脚本编写、数据可视化、或新增/修改 manifests、docs/performance、test/e2e 中与基准测试相关的文件时，加载此指令
applyTo: '**/{manifests,monitoring,performance,e2e}/**'
---

# 研究生论文 — 分布式调度器性能对比实验指令

## 1. 实验总览

### 1.1 研究目标

验证**独立 Binder（Embedded Binder）架构**相较于**共享 Binder（Shared Binder）架构**在 9 个评测维度（吞吐量、延迟、容错、成功率、利用率、碎片化、均衡度、稳定性、扩展性）的系统性提升，同时与 **Volcano**、**Koordinator** 横向对比。

### 1.2 五组对比设计

| 标识 | 配置 | 说明 |
|------|------|------|
| **A — 共享 Binder（Baseline）** | `--enable-embedded-binder=false` + 独立 Binder replicas=1 | 原始 Gödel 架构 |
| **B — 独立 Binder（Proposed）** | `--enable-embedded-binder=true` + Binder replicas=0 | 论文提出的架构 |
| **C — kube-scheduler** | 原生 K8s 调度器（单实例） | 行业基准参考 |
| **D — Volcano** | v1.9.x（单实例） | CNCF 批量调度参考 |
| **E — Koordinator** | v1.5.x（单实例） | 阿里混合调度参考 |

> 所有实验至少执行 **3 次**，取**中位数 + 标准差**。

### 1.3 部署命令

```bash
# 组 A：共享 Binder
kubectl apply -k manifests/base/

# 组 B：独立 Binder
kubectl apply -k manifests/overlays/embedded-binder/

# 组 C：kube-scheduler — 禁用 Gödel，启用原生 kube-scheduler

# 组 D：Volcano
helm install volcano volcano-sh/volcano -n volcano-system --create-namespace --set scheduler.replicas=1

# 组 E：Koordinator
helm install koordinator koordinator-sh/koordinator -n koordinator-system --create-namespace --set scheduler.replicas=1
```

---

## 2. 实验环境

### 2.1 硬件与软件

| 项目 | 规格 |
|------|------|
| CPU | ≥ 48 vCPU（推荐 64 vCPU） |
| 内存 | ≥ 128 GB（推荐 192 GB） |
| 存储 | ≥ 500 GB NVMe SSD |
| OS | Ubuntu 22.04 LTS (amd64) |
| Kubernetes | 1.29.x |
| KWOK | 最新稳定版 |
| Prometheus | v2.51.0（NodePort 30090） |
| Go | 1.21+ |

### 2.2 集群规模梯度

| 编号 | 节点数 | Scheduler 实例数 | 说明 |
|------|--------|------------------|------|
| S1 | 100 | 1 | 小规模基线 |
| S2 | 1,000 | 3 | 中等规模 |
| S3 | 5,000 | 3 | 大规模 |
| S4 | 10,000 | 3 | 超大规模 |
| S5 | 30,000 | 3 | 极限规模 |

---

## 3. 负载模型

### 3.1 负载场景矩阵（8 种）

| 场景 ID | 场景名称 | Pod 创建速率 | 总 Pod 数 | Pod 资源规格 | PodGroup |
|---------|----------|-------------|-----------|-------------|----------|
| W1 | 低负载稳态 | 100 pods/s | 10,000 | cpu:100m, mem:128Mi | 无 |
| W2 | 中负载稳态 | 500 pods/s | 50,000 | cpu:100m, mem:128Mi | 无 |
| W3 | 高负载稳态 | 1,000 pods/s | 100,000 | cpu:100m, mem:128Mi | 无 |
| W4 | 极限负载 | 2,000 pods/s | 200,000 | cpu:100m, mem:128Mi | 无 |
| W5 | 突发洪峰 | 0→2000→0 pods/s | 50,000 | cpu:100m, mem:128Mi | 无 |
| W6 | Gang 调度 | 200 groups/s | 10,000 pods | cpu:100m, mem:128Mi | 5 pods/group |
| W7 | 异构资源 | 500 pods/s | 50,000 | 混合 | 无 |
| W8 | 大规模集群 | 2,000 pods/s | 800,000 | cpu:100m, mem:128Mi | 无 |

**W7 异构资源混合比例**：30% 小(50m/64Mi) + 40% 中(200m/256Mi) + 20% 大(1000m/1Gi) + 10% 超大(4000m/8Gi)

**组 D/E 仅执行核心场景 W1–W4 + W6**。`schedulerName` 按组切换：A/B → `godel-scheduler`，C → `default-scheduler`，D → `volcano`，E → `koord-scheduler`。

### 3.2 Pod 模板

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

### 3.3 Pod 创建工具

使用批量 Pod 创建脚本确保稳定创建速率：

```bash
#!/bin/bash
# bench-create-pods.sh
RATE=${1:-500}; TOTAL=${2:-50000}; NAMESPACE="bench"
kubectl create namespace $NAMESPACE --dry-run=client -o yaml | kubectl apply -f -
for i in $(seq 1 $TOTAL); do
  sed "s/\${INDEX}/$i/g; s/\${CPU}/100/g; s/\${MEM}/128/g" bench-pod-template.yaml | kubectl apply -f - &
  if (( i % RATE == 0 )); then sleep 1; fi
done
wait
```

---

## 4. 九维指标体系

所有指标通过 Prometheus（NodePort 30090）采集，采集间隔 15s。

### 4.1 维度一：吞吐量 (Throughput)

| 指标 | Prometheus 查询 | 目标 |
|------|----------------|------|
| 调度吞吐量（E2E） | `sum(rate(scheduler_pod_scheduling_attempts{result="scheduled"}[1m]))` | B > A |
| 绑定吞吐量（Pod 级） | `sum(godel:binder_embedded_bind_pods:rate1m{result="success"})` | B > A |
| 绑定吞吐量（Unit 级） | `sum(godel:binder_embedded_bind_units:rate1m{result="success"})` | B > A |
| 峰值吞吐量 | `max_over_time(sum(rate(scheduler_pod_scheduling_attempts{result="scheduled"}[1m]))[30m:])` | B > A |

**图表**：T-1 折线图（时序吞吐量）、T-2 柱状图（规模 × 峰值）、T-3 柱状图（场景 × 平均）、T-4 折线图（Scheduler 实例数 × 吞吐量）

### 4.2 维度二：延迟 (Latency)

| 指标 | Prometheus 查询 | 目标 |
|------|----------------|------|
| E2E 调度延迟 P50/P90/P99/AVG | `godel:scheduler_e2e_scheduling_duration:p{50,90,99}` / `:avg` | B < A |
| 绑定延迟 P50/P90/P99（Pod 级） | `godel:binder_embedded_bind_pod_duration:p{50,90,99}` | B < A |
| 调度+绑定组合 P90/P99 | `godel:e2e_schedule_and_bind_duration:p{90,99}_estimate` | B < A |
| 核心算法延迟 P90/P99 | `godel:scheduler_scheduling_algorithm_duration:p{90,99}` | B ≈ A（无退化） |
| 队列等待时间 | `histogram_quantile(0.90, rate(scheduler_pod_pending_in_queue_duration_seconds_bucket[5m]))` | B < A |

**图表**：L-1 折线图（P99 时序）、L-2 分组柱状图（百分位绑定延迟）、L-3 箱线图（E2E 延迟分布）、L-4 折线图（延迟 vs 负载）、L-5 CDF 曲线、L-6 堆积柱状图（Pipeline 分解）

### 4.3 维度三：容错率 (Fault Tolerance)

**故障注入场景**：

| 编号 | 故障类型 | 注入方式 | 预期差异 |
|------|---------|---------|---------|
| F1 | Binder 进程崩溃 | `kubectl delete pod <binder-pod>` | A：全局停止；B：仅影响一个 Scheduler |
| F2 | Scheduler 崩溃 | `kubectl delete pod <scheduler-pod>` | B：该 Binder 同时停止但其他正常 |
| F3 | 节点重新分区 | 触发 Node Shuffler | B：NodeValidator 拦截过期绑定 |
| F4 | API Server 短暂不可用 | 网络隔离 3s | B：指数退避重试 |
| F5 | API Server 高延迟 | 注入 100ms 延迟 | B：重试增加但不崩溃 |

**指标**：MTTR、调度中断时长、丢失绑定请求数、Dispatcher 回退次数、节点验证拦截次数、inflight 绑定数

**图表**：F-1 折线图（故障期间吞吐量）、F-2 柱状图（恢复时间）、F-3 热力图（故障影响范围）

### 4.4 维度四：调度成功率

| 指标 | Prometheus 查询 | 目标 |
|------|----------------|------|
| 调度成功率 | `scheduled / total` | B ≥ A |
| 调度失败率 | `error / total` | B ≤ A |
| 绑定成功率（Pod/Unit 级） | `godel:binder_embedded_bind_{pods,units}:success_rate5m` | B > A |
| API 重试速率 | `godel:binder_embedded_bind_retries:rate5m` | B < A |
| Pending Pod 数量/滞留时间 | `sum(scheduler_pending_pods)` / queue duration P90 | B < A |

**图表**：S-1 折线图（成功率时序）、S-2 分组柱状图（场景 × 成功/失败）、S-3 折线图（Pending Pod）、S-4 柱状图（重试次数）

### 4.5 维度五：资源利用率 — 集群均衡利用

> KWOK 节点无真实资源消耗，通过 `Requested / Allocatable` 计算"调度层面的利用率"。

| 指标 | 目标 |
|------|------|
| 平均 CPU 利用率 | B ≥ A |
| CPU 利用率方差 / 变异系数 (CV) | B < A（更均衡） |
| Memory 利用率方差 | B < A |
| 节点利用率基尼系数 | B < A（0=完全均衡） |

**图表**：U-1 箱线图（利用率分布）、U-2 折线图（方差 × 时间）、U-3 热力图（节点 × 时间）、U-4 柱状图（变异系数）

### 4.6 维度六：资源碎片化

| 指标 | 目标 |
|------|------|
| 资源浪费比例 | B ≤ A |
| 碎片化指数（利用率 < 50% 且 > 0 的节点比例） | B < A |
| 不可用资源占比（剩余资源不足以放置最小 Pod） | B < A |
| 最大可调度 Pod 数 | B ≥ A |

**图表**：R-1 堆积柱状图（资源分配结构）、R-2 柱状图（碎片化指数）、R-3 散点图（CPU vs Memory 利用率）

### 4.7 维度七：Pod 分布均衡度

**论文意义**：验证独立 Binder **不影响** Dispatcher 的节点分区均衡效果。

| 指标 | 目标 |
|------|------|
| 每节点 Pod 数方差 / 变异系数 | B ≤ A |
| Pod/节点比例最大偏差 | B ≤ A |
| Jain 公平性指数 $J = \frac{(\sum x_i)^2}{n \cdot \sum x_i^2}$ | B ≥ A |

**图表**：D-1 箱线图（每节点 Pod 数分布）、D-2 分组柱状图（每 Scheduler 分区 Pod 数）、D-3 直方图（Pod 数分布叠加）

### 4.8 维度八：稳定性 & 抗压能力

**实验设计**：

| 编号 | 方法 | 持续时间 |
|------|------|----------|
| ST-1 | W3（1000 pods/s）持续运行 | 30 min |
| ST-2 | 每 5min 增 200 pods/s（100→2000） | 50 min |
| ST-3 | 200 pods/s → 突发 2000 pods/s × 60s → 回落 | 10 min |
| ST-4 | W2（500 pods/s）长时间运行 | 120 min |

**指标**：P99 延迟趋势斜率（B ≈ 0，A > 0）、吞吐量随时间趋势、goroutine 增长、Pending Pod 堆积、inflight 绑定峰值

**图表**：ST-1~4 折线图

### 4.9 维度九：扩展性 (Scalability)

**水平扩展**（Scheduler 实例数 = 1, 2, 3, 5，固定 5000 nodes + W3）：

| 指标 | 理想值 | 目标 |
|------|--------|------|
| 吞吐量扩展比 `throughput(N)/throughput(1)` | N | B 接近 N，A < N |
| 延迟扩展比 `latency(N)/latency(1)` | 1 | B ≈ 1，A > 1 |
| 扩展效率 `throughput(N)/(N×throughput(1))` | 1 | B > A |

**垂直扩展**（节点数 = 100, 1K, 5K, 10K, 30K，固定 3 Scheduler）

**图表**：SC-1~3（水平扩展曲线）、VS-1~2（垂直扩展曲线）

---

## 5. 实验执行规范

### 5.1 六阶段执行流程

```
Phase 0: 环境准备 (1.5 天) — 集群搭建、KWOK、Prometheus、脚本、部署 Volcano/Koordinator
Phase 1: 组 A 基线实验 (2 天) — W1–W8 + F1–F5 + SC-1~4 + VS-1~5
Phase 2: 组 B 对比实验 (2 天) — 重复 Phase 1 全部 + embedded binder 专有指标
Phase 3: 组 C kube-scheduler (1 天) — W1–W4 + VS-1~5
Phase 4: 组 D Volcano (1.5 天) — W1–W4 + W6 + VS + ST
Phase 5: 组 E Koordinator (1.5 天) — W1–W4 + W6 + VS + ST
Phase 6: 数据分析 + 论文撰写 (4 天)
总计 ~13.5 天
```

### 5.2 单次实验 SOP

```bash
#!/bin/bash
# run-experiment.sh <config> <workload> <run_id>
CONFIG=$1; WORKLOAD=$2; RUN_ID=$3
RESULTS_DIR="results/${CONFIG}/${WORKLOAD}/run${RUN_ID}"
mkdir -p $RESULTS_DIR

# 1. 清理 → 2. 等待稳定(30s) → 3. 记录开始时间
kubectl delete namespace bench --ignore-not-found && sleep 30
START_TIME=$(date +%s)

# 4. 执行负载 → 5. 等待全部调度完成
bash bench-create-pods.sh $RATE $TOTAL
while [[ $(kubectl get pods -n bench --field-selector=status.phase=Pending --no-headers | wc -l) -gt 0 ]]; do sleep 5; done

# 6. 记录结束时间 → 7. 导出 Prometheus 数据 → 8. 采集分布快照
END_TIME=$(date +%s)
bash export-prometheus-data.sh $START_TIME $END_TIME $RESULTS_DIR
bash collect-node-utilization.sh > $RESULTS_DIR/utilization.csv
bash collect-pod-distribution.sh > $RESULTS_DIR/pod-distribution.csv
```

### 5.3 Prometheus 数据导出

导出 15 个核心查询（吞吐量 × 2 + 延迟百分位 × 6 + 成功/失败率 × 2 + Pending + Binder 专有 × 3 + goroutines），使用 `query_range` API，step=15s，输出 JSON。

---

## 6. 论文图表汇总（28+ 张）

| 维度 | 编号 | 图表名称 | 类型 |
|------|------|----------|------|
| 吞吐量 | T-1~T-4 | 时序/规模/场景/扩展 | 折线图/柱状图 |
| 延迟 | L-1~L-6 | P99 时序/百分位/箱线/vs 负载/CDF/Pipeline 分解 | 混合 |
| 容错 | F-1~F-3 | 故障吞吐量/恢复时间/热力图 | 混合 |
| 成功率 | S-1~S-4 | 成功率/场景分解/Pending/重试 | 混合 |
| 利用率 | U-1~U-4 | 分布/方差/热力图/CV | 混合 |
| 碎片化 | R-1~R-3 | 资源结构/碎片指数/散点 | 混合 |
| 均衡度 | D-1~D-3 | Pod 分布/分区 Pod 数/直方图 | 混合 |
| 稳定性 | ST-1~ST-4 | 长时间运行曲线 | 折线图 |
| 扩展性 | SC-1~SC-3 + VS-1~VS-2 | 水平/垂直扩展 | 折线图/柱状图 |

---

## 7. 预期结论

| 维度 | 预期结论 | 论证依据 |
|------|----------|----------|
| **吞吐量** | B ≈ 1.5–3× A；B > D、E | 消除共享 Binder 单点队列竞争 |
| **延迟** | B 绑定延迟降低 50%+，E2E 降低 30–50% | 去除网络开销和队列等待 |
| **容错** | B MTTR 秒级，A 分钟级 | 故障仅影响单个 Scheduler 分区 |
| **成功率** | B 高负载下比 A 高 2–5% | 更快绑定 → Cache 更快更新 → 减少冲突 |
| **利用率** | B 的 CV 比 A 降低 10–20% | 更及时的绑定使全局视图更准确 |
| **碎片化** | B 与 A 持平或略优 | 调度算法不变，绑定更快减少临时碎片 |
| **均衡度** | B 与 A 持平 | Dispatcher Pod 分发策略不变 |
| **稳定性** | B P99 延迟无上升趋势，A 可能上升 | 无队列积压 |
| **扩展性** | B 扩展效率 > 85%（近线性），A < 60% | 独立 Binder 消除共享资源竞争 |

---

## 8. 实验控制变量

| 变量 | 保持一致 |
|------|----------|
| Kubernetes 版本 | 所有组同一版本 |
| API Server 配置 | `--max-mutating-requests-inflight=10000 --max-requests-inflight=20000` |
| 节点规格 | KWOK 节点统一 CPU: 32, Memory: 64Gi |
| Pod 模板 | 相同 resource requests（仅 `schedulerName` 按组切换） |
| 日志级别 | 统一 4 |
| QPS/Burst | 统一 10000 |
| 调度器实例数 | 组 C/D/E 单实例；A/B 按矩阵设置 |

---

## 9. 常见陷阱

1. **KWOK 节点的利用率** = `Requested/Allocatable`（非真实使用），论文中需说明
2. **API Server 瓶颈**：高负载实验需保证 API Server 有充足 CPU/内存
3. **Prometheus 采集间隔**：15s 在突发场景可能丢失瞬态，可临时降至 5s
4. **kind 集群限制**：超大规模测试需使用高配 VM + 物理集群
5. **冷启动效应**：每次实验前等待 30s 稳定，正式数据排除前 60s
6. **GC 干扰**：多次测量取中位数
7. **Volcano 部署**：`vc-controller-manager` + `vc-scheduler` 需充足资源，CRD 不与 Gödel 冲突
8. **Koordinator 部署**：`koordlet` DaemonSet 无法在 KWOK 节点运行，需纯调度器模式测试
9. **调度器指标差异**：Volcano/Koordinator 指标名与 Gödel 不同，需分别配置 scrape + 指标映射

---

## 10. 统计显著性

每个实验重复 **3–5 次**，报告：
- **中位数**（主要值）+ **标准差**（误差线）
- **Mann-Whitney U 检验**（p < 0.05 为显著）
- **效应量**（Cohen's d 或改善百分比）

---

## 11. 数据可视化规范

### 11.1 Python 绘图统一风格

```python
matplotlib.rcParams.update({
    'font.family': 'serif', 'font.serif': ['Times New Roman'], 'font.size': 12,
    'axes.labelsize': 14, 'legend.fontsize': 11, 'figure.dpi': 300,
})

COLORS = {
    'A (Shared Binder)': '#E74C3C',   # 红
    'B (Embedded Binder)': '#2ECC71',  # 绿
    'C (kube-scheduler)': '#3498DB',   # 蓝
    'D (Volcano)': '#F39C12',          # 橙
    'E (Koordinator)': '#9B59B6',      # 紫
}
```

### 11.2 工具链

| 工具 | 用途 |
|------|------|
| Grafana | 实时仪表盘 + 截图 |
| matplotlib / seaborn | 论文级出版质量图表 |
| Prometheus HTTP API | 数据导出 |
| pandas + numpy | 方差/基尼/CV 计算 |
| scipy.stats | Mann-Whitney U 检验 |

---

## 12. 论文章节映射

```
第 4 章 系统设计与实现
├── 4.1 共享 Binder 问题分析    → BINDER_ARCHITECTURE_REFACTORING.md §2
├── 4.2 独立 Binder 架构设计    → Phase 1–4 改造
├── 4.3 容错机制               → NodeValidator + Retry + Fallback
└── 4.4 可观测性设计           → Phase 6–7 (8 指标 + 27 Recording Rules)

第 5 章 实验评估
├── 5.1 环境与方法             → §2–3
├── 5.2 吞吐量 → T-1~T-4      ├── 5.3 延迟 → L-1~L-6
├── 5.4 容错 → F-1~F-3        ├── 5.5 成功率 → S-1~S-4
├── 5.6 资源效率 → U/R/D       ├── 5.7 稳定性 → ST-1~ST-4
├── 5.8 扩展性 → SC/VS         ├── 5.9 横向对比 → D/E 系列
└── 5.10 结论                  → §7 汇总
```

---

## 13. Prometheus Recording Rules 速查

### 13.1 `embedded_binder` 组（19 条）

```
godel:binder_embedded_bind_pods:rate{1m,5m}          — Pod 绑定吞吐量
godel:binder_embedded_bind_units:rate{1m,5m}         — Unit 绑定吞吐量
godel:binder_embedded_bind_duration:p{50,90,99,avg}  — Unit 绑定延迟
godel:binder_embedded_bind_pod_duration:p{50,90,99,avg} — 单 Pod 绑定延迟
godel:binder_embedded_bind_{pods,units}:success_rate5m — 绑定成功率
godel:binder_embedded_bind_retries:rate5m            — API 重试速率
godel:binder_dispatcher_fallback:rate5m              — Dispatcher 回退速率
godel:binder_node_validation_failures:rate5m         — 节点验证失败速率
```

### 13.2 `scheduler_pipeline` 组（8 条）

```
godel:scheduler_pod_scheduling_attempts:rate{1m,5m}  — 调度吞吐量
godel:scheduler_e2e_scheduling_duration:p{50,90,99,avg} — E2E 调度延迟
godel:scheduler_scheduling_algorithm_duration:p{90,99}  — 核心算法延迟
```

### 13.3 `e2e_pipeline` 组（2 条）

```
godel:e2e_schedule_and_bind_duration:p{90,99}_estimate — 调度+绑定组合延迟
```

---

## 14. 关键文件索引

| 文件/目录 | 用途 |
|-----------|------|
| `manifests/base/` | 组 A 共享 Binder 部署 |
| `manifests/overlays/embedded-binder/` | 组 B 独立 Binder 部署（3 个 patch 文件） |
| `manifests/monitoring/` | Prometheus 自动部署（6 个文件） |
| `pkg/binder/metrics/embedded_binder_metrics.go` | 8 个 Binder 指标定义 |
| `pkg/binder/embedded_binder.go` | EmbeddedBinder 核心实现 |
| `pkg/binder/node_validator.go` | 绑定前节点验证 |
| `pkg/binder/cache_adapter.go` | SchedulerCache → BinderCache 适配器 |
| `pkg/binder/utils/retry.go` | 重试 + Dispatcher 回退逻辑 |
| `cmd/scheduler/app/server.go` | Scheduler 启动入口（含 EmbeddedBinder 初始化） |
| `cmd/scheduler/app/options/options.go` | CLI 参数定义 |
| `docs/performance/best-practice.md` | 官方大规模性能测试参考 |
| `docs/performance/node.yaml` | KWOK 节点模板 |
| `THESIS_COMPARISON_PLAN.md` | 完整实验方案文档（本指令的源文件） |
| `BINDER_ARCHITECTURE_REFACTORING.md` | 架构改造设计文档 |
| `BINDER_REFACTORING_PROGRESS.md` | 7 Phase 改造进度记录 |