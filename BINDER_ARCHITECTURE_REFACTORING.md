# Gödel Scheduler Binder 架构重构方案

## 1. 概述

### 1.1 背景

当前 Gödel Scheduler 采用**共享 Binder** 架构，所有 Scheduler 实例共用一个 Binder 进行 Pod 绑定操作。随着集群规模扩大和调度吞吐量需求增加，共享 Binder 成为性能瓶颈。

### 1.2 目标

将 Binder 从共享架构改为**独立 Binder** 架构，即每个 Scheduler 拥有独立的 Binder 实例，以提升调度性能和系统可扩展性。

---

## 2. 架构对比

### 2.1 当前架构（共享 Binder）

```
┌─────────────────────────────────────────────────────────┐
│                      Dispatcher                          │
│  ┌─────────────────┐  ┌─────────────────────────────┐   │
│  │    Scheduler    │  │         Node                │   │
│  │   Maintainer    │  │        Shuffler             │   │
│  └─────────────────┘  └─────────────────────────────┘   │
└─────────────────────────────────────────────────────────┘
                           │
          ┌────────────────┼────────────────┐
          ▼                ▼                ▼
   ┌─────────────┐  ┌─────────────┐  ┌─────────────┐
   │ Scheduler A │  │ Scheduler B │  │ Scheduler C │
   │  (Node      │  │  (Node      │  │  (Node      │
   │  Partition  │  │  Partition  │  │  Partition  │
   │     1)      │  │     2)      │  │     3)      │
   └─────────────┘  └─────────────┘  └─────────────┘
          │                │                │
          └────────────────┼────────────────┘
                           ▼
                  ┌─────────────────┐
                  │  Shared Binder  │  ◄── 性能瓶颈
                  │   (单实例)       │
                  └─────────────────┘
                           │
                           ▼
                  ┌─────────────────┐
                  │   API Server    │
                  └─────────────────┘
```

**问题分析：**

| 问题       | 描述                                     |
| ---------- | ---------------------------------------- |
| 单点瓶颈   | 所有绑定请求经过单一 Binder，吞吐量受限  |
| 队列竞争   | 多个 Scheduler 竞争同一绑定队列          |
| 故障影响大 | Binder 故障导致所有调度停止              |
| 扩展性差   | Scheduler 数量增加时绑定能力不能线性扩展 |

### 2.2 目标架构（独立 Binder）

```
┌─────────────────────────────────────────────────────────┐
│                      Dispatcher                          │
│  ┌─────────────────┐  ┌─────────────────────────────┐   │
│  │    Scheduler    │  │         Node                │   │
│  │   Maintainer    │  │        Shuffler             │   │
│  └─────────────────┘  └─────────────────────────────┘   │
└─────────────────────────────────────────────────────────┘
                           │
          ┌────────────────┼────────────────┐
          ▼                ▼                ▼
   ┌─────────────┐  ┌─────────────┐  ┌─────────────┐
   │ Scheduler A │  │ Scheduler B │  │ Scheduler C │
   │ ┌─────────┐ │  │ ┌─────────┐ │  │ ┌─────────┐ │
   │ │ Binder  │ │  │ │ Binder  │ │  │ │ Binder  │ │
   │ │   A     │ │  │ │   B     │ │  │ │   C     │ │
   │ └─────────┘ │  │ └─────────┘ │  │ └─────────┘ │
   │  (Node      │  │  (Node      │  │  (Node      │
   │  Partition  │  │  Partition  │  │  Partition  │
   │     1)      │  │     2)      │  │     3)      │
   └─────────────┘  └─────────────┘  └─────────────┘
          │                │                │
          ▼                ▼                ▼
   ┌─────────────────────────────────────────────────────┐
   │                    API Server                        │
   └─────────────────────────────────────────────────────┘
```

**优势分析：**

| 优势     | 描述                                     |
| -------- | ---------------------------------------- |
| 消除瓶颈 | 绑定请求并行处理，吞吐量线性扩展         |
| 无竞争   | 每个 Binder 独立队列，无锁竞争           |
| 故障隔离 | 单个 Binder 故障只影响对应 Scheduler     |
| 简化通信 | Scheduler 和 Binder 同进程，减少网络开销 |
| 状态一致 | 共享 Cache，Assumed Pods 状态天然同步    |

---

## 3. 可行性分析

### 3.1 节点分区机制保障

Gödel Scheduler 已有**节点分区（Node Partition）** 机制，由 Node Shuffler 组件管理：

```go
// pkg/dispatcher/node-shuffler/node_shuffler.go

// NodeShuffler 负责节点分区管理
type NodeShuffler struct {
    schedulerMaintainer *schemaintainer.SchedulerMaintainer
    k8sClient           kubernetes.Interface
    crdClient           crdclient.Interface
    nodeLister          corelister.NodeLister
    nmNodeLister        nodelister.NMNodeLister
    nodeProcessingQueue *nodeQueue
}

// 节点通过注解分配给特定 Scheduler
const GodelSchedulerNodeAnnotationKey = "godel.bytedance.com/scheduler-name"
```

**分区机制工作原理：**

1. **节点分配**：每个节点通过 `godel.bytedance.com/scheduler-name` 注解分配给特定 Scheduler
2. **负载均衡**：Node Shuffler 定期检查并重新平衡各 Scheduler 的节点数量
3. **隔离保障**：每个 Scheduler 只调度其分区内的节点

```go
// ReBalanceSchedulerNodes 重新平衡节点分布
func (ns *NodeShuffler) ReBalanceSchedulerNodes() {
    schedulerInfo := ns.schedulerMaintainer.GetSchedulersWithMostAndLeastNumberOfNodes()
    if schedulerInfo.MostNumberOfNodes > schedulerInfo.LeastNumberOfNodes*2 {
        // 从节点最多的 Scheduler 移动部分节点到节点最少的 Scheduler
        numberOfNodesNeedToBeMoved := (schedulerInfo.MostNumberOfNodes +
            schedulerInfo.LeastNumberOfNodes) / 2 - schedulerInfo.LeastNumberOfNodes
        // ...
    }
}
```

**结论**：节点分区机制天然保证了不同 Scheduler 调度不同节点，独立 Binder 不会产生资源冲突。

### 3.2 潜在挑战与解决方案

#### 3.2.1 冲突检测

**场景**：节点重新分区期间，可能出现短暂的双写风险。

**解决方案**：

```go
// 方案 A: 乐观并发 + API Server 冲突检测
func (b *Binder) Bind(pod *v1.Pod, nodeName string) error {
    err := b.client.CoreV1().Pods(pod.Namespace).Bind(bindingObj)
    if errors.IsConflict(err) {
        return b.handleConflict(pod, nodeName)
    }
    return err
}

// 方案 B: 绑定前验证节点分区
func (b *Binder) Bind(pod *v1.Pod, nodeName string) error {
    node, err := b.nodeLister.Get(nodeName)
    if err != nil {
        return err
    }
    // 验证节点是否仍属于当前 Scheduler
    if node.Annotations[nodeutil.GodelSchedulerNodeAnnotationKey] != b.schedulerName {
        return fmt.Errorf("node %s no longer in partition of scheduler %s",
            nodeName, b.schedulerName)
    }
    return b.doBind(pod, nodeName)
}
```

#### 3.2.2 全局资源视图

**场景**：每个 Binder 需要感知其他 Scheduler 的绑定结果。

**解决方案**：

```go
// 通过 Informer 监听全局 Pod 事件
type Binder struct {
    podInformer cache.SharedIndexInformer
    cache       *schedulercache.Cache
}

func (b *Binder) setupInformers() {
    b.podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
        UpdateFunc: func(oldObj, newObj interface{}) {
            pod := newObj.(*v1.Pod)
            if pod.Spec.NodeName != "" {
                // 更新本地缓存，感知其他 Scheduler 的绑定
                b.cache.UpdatePodBinding(pod)
            }
        },
    })
}
```

#### 3.2.3 Assumed Pods 同步

**场景**：Scheduler 的 Assumed Pods 需要被对应 Binder 知道。

**解决方案**：Scheduler 和 Binder 同进程，共享 Cache。

```go
type SchedulerWithBinder struct {
    scheduler *Scheduler
    binder    *Binder
    cache     *schedulercache.Cache  // 共享 Cache
}
```

---

## 4. 详细设计

### 4.1 核心数据结构修改

#### 4.1.1 Scheduler 结构体

```go
type Scheduler struct {
    // ...existing fields...

    SchedulerName string

    // 新增：内嵌 Binder
    binder *binder.Binder

    // 共享 Cache
    cache *schedulercache.Cache
}

func NewScheduler(
    client kubernetes.Interface,
    informerFactory informers.SharedInformerFactory,
    schedulerName string,
    opts ...Option,
) (*Scheduler, error) {

    // 创建共享 Cache
    cache := schedulercache.New(ttl)

    // 创建内嵌 Binder
    b, err := binder.NewBinder(
        client,
        informerFactory,
        schedulerName,
        cache,  // 共享 Cache
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create binder: %v", err)
    }

    return &Scheduler{
        SchedulerName: schedulerName,
        binder:        b,
        cache:         cache,
        // ...other fields...
    }, nil
}

func (s *Scheduler) Run(ctx context.Context) {
    // 启动 Binder
    go s.binder.Run(ctx)

    // 启动调度循环
    go s.scheduleLoop(ctx)

    <-ctx.Done()
}
```

#### 4.1.2 Binder 结构体

```go
type Binder struct {
    client        kubernetes.Interface
    schedulerName string

    // 绑定队列（独立）
    bindingQueue workqueue.RateLimitingInterface

    // 共享 Cache（来自 Scheduler）
    cache *schedulercache.Cache

    // Pod Informer（用于感知全局绑定）
    podInformer cache.SharedIndexInformer

    // 节点验证器
    nodeValidator *NodeValidator
}

func NewBinder(
    client kubernetes.Interface,
    informerFactory informers.SharedInformerFactory,
    schedulerName string,
    cache *schedulercache.Cache,
) (*Binder, error) {

    return &Binder{
        client:        client,
        schedulerName: schedulerName,
        bindingQueue: workqueue.NewNamedRateLimitingQueue(
            workqueue.DefaultControllerRateLimiter(),
            fmt.Sprintf("binder-%s", schedulerName),
        ),
        cache:       cache,
        podInformer: informerFactory.Core().V1().Pods().Informer(),
        nodeValidator: NewNodeValidator(
            informerFactory.Core().V1().Nodes().Lister(),
            schedulerName,
        ),
    }, nil
}

func (b *Binder) Run(ctx context.Context) {
    defer b.bindingQueue.ShutDown()

    // 启动 workers
    for i := 0; i < BinderWorkerCount; i++ {
        go wait.UntilWithContext(ctx, b.worker, time.Second)
    }

    <-ctx.Done()
}

func (b *Binder) worker(ctx context.Context) {
    for b.processNextItem(ctx) {
    }
}

func (b *Binder) processNextItem(ctx context.Context) bool {
    item, shutdown := b.bindingQueue.Get()
    if shutdown {
        return false
    }
    defer b.bindingQueue.Done(item)

    req := item.(*BindRequest)
    if err := b.bind(ctx, req); err != nil {
        klog.ErrorS(err, "Failed to bind pod",
            "pod", klog.KObj(req.Pod),
            "node", req.NodeName)
        b.bindingQueue.AddRateLimited(item)
        return true
    }

    b.bindingQueue.Forget(item)
    return true
}
```

### 4.2 绑定流程

#### 4.2.1 绑定请求结构

```go
type BindRequest struct {
    Pod           *v1.Pod
    NodeName      string
    SchedulerName string
    AssumedAt     time.Time

    // 可选：绑定回调
    Callback func(error)
}
```

#### 4.2.2 绑定逻辑

```go
func (b *Binder) bind(ctx context.Context, req *BindRequest) error {
    // Step 1: 验证节点仍在当前 Scheduler 分区
    if err := b.nodeValidator.Validate(req.NodeName); err != nil {
        return fmt.Errorf("node validation failed: %v", err)
    }

    // Step 2: 执行绑定
    binding := &v1.Binding{
        ObjectMeta: metav1.ObjectMeta{
            Name:      req.Pod.Name,
            Namespace: req.Pod.Namespace,
            UID:       req.Pod.UID,
        },
        Target: v1.ObjectReference{
            Kind: "Node",
            Name: req.NodeName,
        },
    }

    err := b.client.CoreV1().Pods(req.Pod.Namespace).Bind(ctx, binding, metav1.CreateOptions{})
    if err != nil {
        // 绑定失败，从 Cache 中移除 Assumed Pod
        b.cache.ForgetPod(req.Pod)
        return err
    }

    // Step 3: 更新 Cache
    b.cache.FinishBinding(req.Pod)

    // Step 4: 执行回调
    if req.Callback != nil {
        req.Callback(nil)
    }

    klog.V(3).InfoS("Successfully bound pod to node",
        "pod", klog.KObj(req.Pod),
        "node", req.NodeName,
        "scheduler", b.schedulerName)

    return nil
}

// Bind 是供 Scheduler 调用的入口
func (b *Binder) Bind(req *BindRequest) {
    b.bindingQueue.Add(req)
}

// BindSync 同步绑定（用于特殊场景）
func (b *Binder) BindSync(ctx context.Context, req *BindRequest) error {
    return b.bind(ctx, req)
}
```

#### 4.2.3 节点验证器

```go
type NodeValidator struct {
    nodeLister    corelister.NodeLister
    schedulerName string
}

func NewNodeValidator(nodeLister corelister.NodeLister, schedulerName string) *NodeValidator {
    return &NodeValidator{
        nodeLister:    nodeLister,
        schedulerName: schedulerName,
    }
}

func (v *NodeValidator) Validate(nodeName string) error {
    node, err := v.nodeLister.Get(nodeName)
    if err != nil {
        return fmt.Errorf("failed to get node: %v", err)
    }

    schedulerAnnotation := node.Annotations[nodeutil.GodelSchedulerNodeAnnotationKey]
    if schedulerAnnotation != v.schedulerName {
        return fmt.Errorf("node %s belongs to scheduler %s, not %s",
            nodeName, schedulerAnnotation, v.schedulerName)
    }

    return nil
}
```

### 4.3 调度流程集成

```go
func (us *UnitScheduler) scheduleOne(ctx context.Context) {
    // ...existing scheduling logic...

    // 调度成功，假定 Pod 已调度
    if err := us.cache.AssumePod(pod, nodeName); err != nil {
        klog.ErrorS(err, "Failed to assume pod", "pod", klog.KObj(pod))
        return
    }

    // 异步绑定
    us.scheduler.binder.Bind(&binder.BindRequest{
        Pod:           pod,
        NodeName:      nodeName,
        SchedulerName: us.scheduler.SchedulerName,
        AssumedAt:     time.Now(),
        Callback: func(err error) {
            if err != nil {
                klog.ErrorS(err, "Binding failed", "pod", klog.KObj(pod))
                // 可选：通知调度队列重试
            }
        },
    })
}
```

---

## 5. 配置与部署

### 5.1 配置选项

```go
type SchedulerOptions struct {
    // ...existing options...

    // Binder 配置
    BinderConfig BinderConfig
}

type BinderConfig struct {
    // 是否启用内嵌 Binder（新架构）
    EnableEmbeddedBinder bool

    // Binder worker 数量
    BinderWorkerCount int

    // 绑定超时时间
    BindTimeout time.Duration

    // 重试配置
    MaxRetries int
    RetryBackoff time.Duration
}
```

### 5.2 命令行参数

```go
func (o *Options) AddFlags(fs *pflag.FlagSet) {
    // ...existing flags...

    fs.BoolVar(&o.BinderConfig.EnableEmbeddedBinder, "enable-embedded-binder",
        true, "Enable embedded binder in each scheduler instance")

    fs.IntVar(&o.BinderConfig.BinderWorkerCount, "binder-worker-count",
        10, "Number of binder workers per scheduler")

    fs.DurationVar(&o.BinderConfig.BindTimeout, "bind-timeout",
        30*time.Second, "Timeout for binding operations")
}
```

### 5.3 部署方式

#### 5.3.1 新部署方式（推荐）

```yaml
# Scheduler + Binder 合并部署
apiVersion: apps/v1
kind: Deployment
metadata:
  name: godel-scheduler
  namespace: kube-system
spec:
  replicas: 3
  selector:
    matchLabels:
      app: godel-scheduler
  template:
    metadata:
      labels:
        app: godel-scheduler
    spec:
      containers:
        - name: scheduler
          image: godel-scheduler:v1.0.0
          args:
            - --scheduler-name=$(POD_NAME)
            - --enable-embedded-binder=true
            - --binder-worker-count=10
            - --bind-timeout=30s
          env:
            - name: POD_NAME
              valueFrom:
                fieldRef:
                  fieldPath: metadata.name
          resources:
            requests:
              cpu: "2"
              memory: "4Gi"
            limits:
              cpu: "4"
              memory: "8Gi"
```

#### 5.3.2 兼容旧部署方式

```yaml
# 仍然支持独立 Binder（用于迁移过渡）
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: godel-binder
  namespace: kube-system
spec:
  replicas: 1
  template:
    spec:
      containers:
        - name: binder
          image: godel-binder:v1.0.0
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: godel-scheduler
  namespace: kube-system
spec:
  replicas: 3
  template:
    spec:
      containers:
        - name: scheduler
          image: godel-scheduler:v1.0.0
          args:
            - --enable-embedded-binder=false # 使用独立 Binder
```

---

## 6. 性能对比

### 6.1 理论分析

| 指标       | 共享 Binder         | 独立 Binder | 提升比例                   |
| ---------- | ------------------- | ----------- | -------------------------- |
| 绑定吞吐量 | 单实例限制          | N × 单实例  | N 倍（N = Scheduler 数量） |
| 绑定延迟   | 队列等待 + 处理时间 | 仅处理时间  | 50%+ 降低                  |
| 内存占用   | 较低                | 略高        | +20%                       |
| CPU 占用   | 集中                | 分散        | 更均衡                     |
| 故障影响   | 全局                | 局部        | 大幅降低                   |

### 6.2 预期效果

```
场景：3 个 Scheduler，每秒 1000 个 Pod 绑定请求

共享 Binder:
├── 单 Binder 处理所有请求
├── 队列平均长度: ~1000
├── 平均绑定延迟: ~1s
└── 最大吞吐量: ~1000 pods/s

独立 Binder:
├── 每个 Binder 处理 ~333 请求
├── 队列平均长度: ~100
├── 平均绑定延迟: ~100ms
└── 最大吞吐量: ~3000 pods/s
```

---

## 7. 迁移计划

### 7.1 阶段划分

```
Phase 1 (Week 1-2): 接口重构
├── 定义 Binder 接口
├── 支持 Scheduler 关联
└── 添加配置开关

Phase 2 (Week 3-4): 核心实现
├── 实现内嵌 Binder
├── 集成到 Scheduler
└── 共享 Cache 机制

Phase 3 (Week 5-6): 测试验证
├── 单元测试
├── 集成测试
└── 性能基准测试

Phase 4 (Week 7-8): 灰度发布
├── 测试环境验证
├── 灰度生产环境
└── 全量发布

Phase 5 (Week 9-10): 清理收尾
├── 废弃独立 Binder
├── 更新文档
└── 监控告警配置
```

### 7.2 回滚策略

```go
// 通过配置开关支持快速回滚
if !config.EnableEmbeddedBinder {
    // 使用独立 Binder（旧架构）
    return scheduler.NewSchedulerWithExternalBinder(...)
}
// 使用内嵌 Binder（新架构）
return scheduler.NewSchedulerWithEmbeddedBinder(...)
```

---

## 8. 监控指标

### 8.1 新增指标

```go
var (
    // 绑定延迟分布
    BindingLatency = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "godel_binder_binding_latency_seconds",
            Help:    "Latency of binding operations",
            Buckets: prometheus.ExponentialBuckets(0.001, 2, 15),
        },
        []string{"scheduler", "result"},
    )

    // 绑定队列长度
    BindingQueueLength = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "godel_binder_queue_length",
            Help: "Length of binding queue",
        },
        []string{"scheduler"},
    )

    // 绑定成功/失败计数
    BindingTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "godel_binder_binding_total",
            Help: "Total number of binding attempts",
        },
        []string{"scheduler", "result"},
    )

    // 节点验证失败计数
    NodeValidationFailures = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "godel_binder_node_validation_failures_total",
            Help: "Total number of node validation failures",
        },
        []string{"scheduler", "reason"},
    )
)
```

### 8.2 告警规则

```yaml
groups:
  - name: binder-alerts
    rules:
      - alert: BinderQueueBacklog
        expr: godel_binder_queue_length > 100
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "Binder queue backlog detected"
          description: "Scheduler {{ $labels.scheduler }} has {{ $value }} pending bindings"

      - alert: BindingLatencyHigh
        expr: histogram_quantile(0.99, godel_binder_binding_latency_seconds) > 5
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High binding latency"
          description: "99th percentile binding latency is {{ $value }}s"

      - alert: BindingFailureRateHigh
        expr: |
          rate(godel_binder_binding_total{result="failure"}[5m]) /
          rate(godel_binder_binding_total[5m]) > 0.05
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "High binding failure rate"
          description: "Binding failure rate is {{ $value | humanizePercentage }}"
```

---

## 9. 风险评估

### 9.1 风险矩阵

| 风险                    | 可能性 | 影响 | 缓解措施                            |
| ----------------------- | ------ | ---- | ----------------------------------- |
| 节点分区期间双写冲突    | 低     | 中   | 绑定前节点验证 + API Server 乐观锁  |
| 内存占用增加            | 高     | 低   | 合理配置 worker 数量，共享 Informer |
| 迁移期间兼容性问题      | 中     | 高   | 配置开关支持快速回滚                |
| Assumed Pods 状态不一致 | 低     | 高   | 共享 Cache + Informer 同步          |

### 9.2 兼容性保证

1. **API 兼容**：不改变任何外部 API
2. **行为兼容**：绑定结果与旧架构完全一致
3. **配置兼容**：通过开关支持新旧架构切换
4. **监控兼容**：保留原有指标，新增细粒度指标

---

## 10. 总结

### 10.1 核心改动

1. **Binder 内嵌到 Scheduler**：每个 Scheduler 实例拥有独立的 Binder
2. **共享 Cache**：Scheduler 和 Binder 共享调度缓存
3. **节点验证**：绑定前验证节点仍属于当前 Scheduler 分区
4. **配置开关**：支持新旧架构灵活切换

### 10.2 预期收益

- **性能提升**：绑定吞吐量线性扩展，延迟显著降低
- **可靠性提升**：故障隔离，单点故障影响范围缩小
- **架构简化**：减少组件间通信，简化部署和运维

### 10.3 下一步行动

1. 评审本设计文档
2. 创建相关 Issue 和里程碑
3. 按阶段实施迁移计划
4. 持续监控和优化
