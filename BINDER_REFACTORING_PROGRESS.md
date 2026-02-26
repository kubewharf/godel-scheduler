# 独立 Binder 架构改造进度记录

## Phase 1：接口定义与配置开关

**状态**：✅ 已完成  
**完成日期**：2026-02-26

### 新增文件

| 文件 | 用途 |
|------|------|
| `pkg/binder/binder_interface.go` | `BinderInterface` 接口 + `BindRequest` / `BindResult` 结构体 + 验证逻辑 + 多节点 PodGroup 支持 |
| `pkg/binder/embedded_binder_config.go` | `EmbeddedBinderConfig` 配置结构体 + 默认值 + 验证 |
| `pkg/binder/binder_interface_test.go` | 接口测试（模块 A）— 8 个测试函数，覆盖验证正向/反向用例 |
| `pkg/binder/embedded_binder_config_test.go` | 配置测试（模块 K）— 2 个测试函数，10 个子用例 |

### 修改文件

| 文件 | 改动 |
|------|------|
| `cmd/scheduler/app/options/options.go` | `Options` 新增 `EnableEmbeddedBinder` + `EmbeddedBinderConfig` 字段；注册 `--enable-embedded-binder`、`--max-bind-retries`、`--bind-timeout`、`--max-local-retries` CLI flags；`Validate()` 中条件校验 |
| `cmd/scheduler/app/options/options_test.go` | 新增 4 个测试函数（模块 L）：默认值、flag 解析、自定义值、非法值校验 |

### BindRequest 多节点 PodGroup 支持

部署调试中发现原始 `NodeName string` 单字段设计无法表达 PodGroup 中各 Pod 调度到不同节点的场景，因此增强了 `BindRequest`：

| 新增字段/方法 | 类型 | 说明 |
|--------------|------|------|
| `NodeNames` | `map[types.UID]string` | 按 Pod UID 映射各自的目标节点，支持多节点 PodGroup |
| `NodeNameFor(uid)` | 方法 | 优先查 `NodeNames`，回退到 `NodeName` |
| `UniqueNodeNames()` | 方法 | 返回请求中所有目标节点的去重集合（供 `NodeValidator` 批量验证） |
| `AllFailed()` | `BindResult` 方法 | 判断是否所有 Pod 均绑定失败 |

`Validate()` 增强：当 `NodeNames` 非空时，验证每个 Pod 均有对应条目；`NodeName` 或 `NodeNames` 至少提供一个。

### Phase 1 交付物对照

- [x] 定义 `BinderInterface`（`BindUnit` / `Start` / `Stop`）
- [x] 定义 `BindRequest` / `BindResult` 结构体（含 `Validate()`）
- [x] `--enable-embedded-binder` 开关（默认 `false`，向后兼容）
- [x] `EmbeddedBinderConfig` 及默认值（`MaxBindRetries=3`, `BindTimeout=30s`, `MaxLocalRetries=5`）
- [x] `NodeNames` 多节点映射 + `NodeNameFor()` / `UniqueNodeNames()` 辅助方法
- [x] `BindResult.AllFailed()` 用于区分全失败 / 部分失败
- [x] 模块 A 测试 ≥ 6 个用例 ✓（8 个）
- [x] 模块 K 测试 ≥ 5 个用例 ✓（10 个子用例）
- [x] 模块 L 测试 ≥ 4 个用例 ✓（4 个测试函数，14 个子用例）

### 测试结果

- 全部 PASS，含 `-race` 检测
- `go vet` 无新增告警
- 所有现有测试回归通过

---

## Phase 2：核心集成

**状态**：✅ 已完成  
**完成日期**：2026-02-26

### 新增文件

| 文件 | 用途 | 测试数 |
|------|------|--------|
| `pkg/binder/utils/retry.go` | 绑定失败计数 + Dispatcher 重调度判定（模块 B） | — |
| `pkg/binder/utils/retry_test.go` | retry 逻辑测试 | 6 个函数，19 个子用例 |
| `pkg/binder/utils/util_test.go` | `CleanupPodAnnotations` / `CleanupPodAnnotationsWithRetryCount` 测试 | 8 个函数 |
| `pkg/binder/cache_adapter.go` | `CacheAdapter`：包装 SchedulerCache 为 BinderCache（模块 C） | — |
| `pkg/binder/cache_adapter_test.go` | CacheAdapter 测试，含并发竞态测试 | 18 个函数 |
| `pkg/binder/embedded_binder.go` | `EmbeddedBinder`：实现 `BinderInterface`（模块 E） | — |
| `pkg/binder/embedded_binder_test.go` | EmbeddedBinder 测试，含冲突重试、超时、并发 | 16 个函数 |
| `pkg/scheduler/embedded_binder_integration_test.go` | Scheduler 层嵌入式 Binder 集成测试 | 4 个函数 |
| `pkg/scheduler/core/unit_scheduler/embedded_binder_test.go` | UnitScheduler 嵌入式绑定路径测试 | 7 个函数 |

### 修改文件

| 文件 | 改动 |
|------|------|
| `pkg/binder/utils/util.go` | 提取 `cleanupSchedulingAnnotations()` 辅助函数；新增 `CleanupPodAnnotationsWithRetryCount()` 含重试计数感知的 Dispatcher 回退逻辑；fallback 分支接入 `ObserveDispatcherFallback()` 指标 |
| `pkg/scheduler/scheduler.go` | 新增 `embeddedBinder` 字段；`SetEmbeddedBinder`/`GetEmbeddedBinder`/`GetCache` 方法；`createDataSet()` 中向 UnitScheduler 传播 Binder；**`SetEmbeddedBinder` 增加 `RangeDataSets` 追溯传播**（修复启动时序问题） |
| `pkg/scheduler/core/types.go` | `UnitScheduler` 接口新增 `SetEmbeddedBinder`/`GetEmbeddedBinder` 方法 |
| `pkg/scheduler/core/unit_scheduler/unit_scheduler.go` | 新增 `embeddedBinder` 字段及 setter/getter；将 `PersistSuccessfulPods` 拆分为 `persistViaEmbeddedBinder`（直接绑定路径）和 `persistViaPatchPod`（传统 Patch 路径）；**`persistViaEmbeddedBinder` 使用 `ClonedPod.Annotations[AssumedNodeAnnotationKey]` 构建 per-pod `NodeNames` 映射** |
| `cmd/scheduler/app/config/config.go` | `Config` 新增 `EnableEmbeddedBinder` 和 `EmbeddedBinderConfig` 字段 |
| `cmd/scheduler/app/options/options.go` | `ApplyTo()` 中传播嵌入式 Binder 配置到 Config |
| `cmd/scheduler/app/server.go` | `Run()` 中按配置创建 `EmbeddedBinder` 并注入到 Scheduler；**调用 `bindermetrics.Register()` 注册 Binder 指标到全局 registry**；**调用 `eb.Start(ctx)` 启动 Binder 后再 `SetEmbeddedBinder(eb)`**（修复 `ErrBinderNotRunning`） |

### Phase 2 交付物对照

- [x] **模块 B**：`retry.go` — 绑定失败计数（`Get/Set/IncrementBindFailureCount`）+ Dispatcher 重调度判定（`ShouldDispatchToAnotherScheduler`）+ 失败原因记录
- [x] **模块 C**：`cache_adapter.go` — `CacheAdapter` 将 SchedulerCache 包装为 BinderCache 接口（`AssumePod`/`ForgetPod`/`FinishBinding`/`MarkPodToDelete` 等）
- [x] **模块 E**：`embedded_binder.go` — 完整实现 `BinderInterface`（`BindUnit` 含 Conflict/Timeout 指数退避重试；`Start`/`Stop` 原子生命周期管理；含 inflight gauge / per-pod timing / retry counter 指标埋点）
- [x] **模块 F**：`scheduler.go` 集成 — Scheduler 持有并管理 EmbeddedBinder 生命周期；`createDataSet` 自动传播到 UnitScheduler；`SetEmbeddedBinder` 具备 `RangeDataSets` 追溯传播能力
- [x] **模块 G**：`unit_scheduler.go` 绑定路径修改 — `PersistSuccessfulPods` 根据 `embeddedBinder` 是否存在分流到直接绑定 / 传统 Patch 路径；`persistViaEmbeddedBinder` 从 `ClonedPod.Annotations[AssumedNodeAnnotationKey]` 提取节点名，构建 per-pod `NodeNames` 映射
- [x] **模块 H**：`server.go` + `config.go` + `options.go` 端到端串联 — CLI 参数 → Config → Server → Scheduler → UnitScheduler 完整链路；`eb.Start(ctx)` 在 `SetEmbeddedBinder` 之前调用
- [x] `util.go` 增强 — `CleanupPodAnnotationsWithRetryCount` 支持按重试次数回退到 Dispatcher
- [x] 全部新增代码测试覆盖 — 共 59 个新测试函数/用例，全部 PASS（含 `-race`）

### 部署调试修复记录

| # | 问题现象 | 根因 | 修复 |
|---|---------|------|------|
| 1 | `persistViaEmbeddedBinder` 调用时 `embeddedBinder == nil` | `New()` 中 `createDataSet()` 在 `SetEmbeddedBinder()` 之前执行，导致已创建的 `ScheduleDataSet` 中 unitScheduler 的 `embeddedBinder` 为 nil | `SetEmbeddedBinder` 增加 `sched.ScheduleSwitch.RangeDataSets()` 循环，追溯传播到所有已存在的 unitScheduler 实例 |
| 2 | PodGroup 含多 Pod 时 `NodeName` 无法表达不同 Pod 分配到不同节点 | 原始设计仅有单一 `NodeName` 字段 | `BindRequest` 增加 `NodeNames map[types.UID]string`；`persistViaEmbeddedBinder` 从 `ClonedPod.Annotations[AssumedNodeAnnotationKey]` 逐 Pod 提取节点名 |
| 3 | `BindUnit` 返回 `ErrBinderNotRunning` | `server.go` 中仅调用了 `NewEmbeddedBinder()` 和 `SetEmbeddedBinder()` 但未调用 `eb.Start(ctx)` | 在 `SetEmbeddedBinder` 之前添加 `eb.Start(ctx)` 调用 |

### 测试结果

- 新增测试全部 PASS（`go test -race -count=1`）
- 全量构建通过：`go build ./cmd/scheduler/... ./cmd/binder/...`
- 现有测试中 `TestSchedulerEvent`、`TestPrepareNodes` 等少量失败为 **预存问题**（已在 `git stash` 对比中确认与本次改动无关）

---

## Phase 3：PodGroupController 迁移

**状态**：✅ 已完成  
**完成日期**：2026-02-26

### 修改文件

| 文件 | 改动 |
|------|------|
| `pkg/binder/controller/podgroup.go` | 新增 `PodGroupControllerOptions` 结构体（含 `SchedulerName` 字段）；新增 `SetupPodGroupControllerWithOptions` 入口（嵌入模式使用）；保留 `SetupPodGroupController` 向后兼容；新增 `podBelongsToPartition` 分区过滤方法（按 `SchedulerAnnotationKey` 过滤 Pod）；`syncHandler` 中在计算 PodGroup 状态前按分区过滤 Pod；`updatePodGroup` 中增加 `IsConflict` 冲突重试处理（annotation 和 status 更新均支持） |
| `cmd/scheduler/app/server.go` | 当 `EnableEmbeddedBinder` 为 true 时，在 `run()` 中使用 `SetupPodGroupControllerWithOptions` 启动 PodGroupController，传入 `SchedulerName` 实现分区隔离 |
| `pkg/binder/eventhandlers.go` | `addAllEventHandlers` 增加 `embeddedMode bool` 参数；嵌入模式下跳过 BinderQueue 相关的 informer handler 注册（`addPodToBinderQueue`、`updatePodInBinderQueue`、`deletePodFromBinderQueue`）；Cache handler 始终注册 |
| `pkg/binder/godel_binder.go` | 调用 `addAllEventHandlers` 时传入 `false`（独立模式适配） |

### 新增测试文件

| 文件 | 用途 | 测试数 |
|------|------|--------|
| `pkg/binder/controller/podgroup_partition_test.go` | PodGroupController 分区过滤测试 | 8 个测试（含 6 个 `podBelongsToPartition` 子用例 + 分区过滤场景覆盖 + 向后兼容性验证） |
| `pkg/binder/eventhandlers_embedded_test.go` | 嵌入模式 EventHandler 行为测试 | 6 个测试（嵌入模式跳过、独立模式正常、Update/Delete handler、非 Assumed Pod 过滤、错误 Scheduler 过滤） |

### Phase 3 交付物对照

- [x] **PodGroupController 提取**：`SetupPodGroupControllerWithOptions` 新入口，支持通过 `PodGroupControllerOptions` 传入调度器名称
- [x] **分区过滤**：`podBelongsToPartition` 方法按 `SchedulerAnnotationKey` 注解过滤 Pod，`syncHandler` 仅统计属于本分区的 Pod
- [x] **多实例冲突处理**：`updatePodGroup` 中 annotation 和 status 更新均增加 `apierrs.IsConflict` 冲突重试（RefreshGet + Retry）
- [x] **Scheduler 集成**：`cmd/scheduler/app/server.go` 在 `EnableEmbeddedBinder` 时启动 PodGroupController，使用 `SchedulerName` 分区
- [x] **EventHandler 嵌入模式跳过**：`addAllEventHandlers` 在嵌入模式下不注册 BinderQueue handler，避免与 Scheduler 调度队列冲突
- [x] **向后兼容**：`SetupPodGroupController` 保持不变，独立 Binder 模式行为无改动

### 测试结果

- `pkg/binder/...` + `pkg/binder/controller/...`：**全部通过**（含 `-race` 竞态检测）
- `go vet` 无新增告警（`pkg/binder/controller/...`、`cmd/scheduler/...` 均通过）
- 编译验证：`cmd/scheduler`、`cmd/binder` 均正常构建
- 原有 21 个 PodGroupController 测试全部回归通过

---

## Phase 4：容错与节点验证

**状态**：✅ 已完成  
**完成日期**：2026-02-27

### 新增文件

| 文件 | 用途 | 测试数 |
|------|------|--------|
| `pkg/binder/node_validator.go` | `NodeValidator`：绑定前验证目标节点仍属于当前 Scheduler 分区（模块 D）；`NodeGetter` 类型 + `NodeOwnershipError` 类型化错误 + `IsNodeOwnershipError()` 辅助函数；**允许未分区节点通过验证**（注解缺失时视为未分配，不拒绝绑定） | — |
| `pkg/binder/node_validator_test.go` | NodeValidator 测试 | 12 个测试（8 个 Validate 子用例 + NilGetter + ErrorMessage + IsNodeOwnershipError 4 个子用例） |
| `pkg/binder/binder_reconciler_test.go` | BinderTasksReconciler 增强测试 | 9 个测试（Enqueue、Success、TransientError、NotFound、Run/Stop、ConcurrentAdd、DispatcherFallback、LocalRetry、Len） |
| `pkg/binder/metrics/embedded_binder_metrics.go` | 嵌入式 Binder 专用指标：`nodeValidationFailures`、`embeddedBinderBindTotal`、`embeddedBinderBindLatency`、`dispatcherFallbackTotal` | — |

### 修改文件

| 文件 | 改动 |
|------|------|
| `pkg/binder/binder_reconciler.go` | 新增 `schedulerName` / `maxLocalRetries` 字段；新增 `NewBinderTaskReconcilerWithRetry()` 重试感知构造函数；新增 `Len()` 方法；新增 `cleanupPod()` 方法（按配置分派到 `CleanupPodAnnotations` 或 `CleanupPodAnnotationsWithRetryCount`）；`APICallFailedWorker` 使用 `cleanupPod()` |
| `pkg/binder/embedded_binder.go` | 新增 `nodeValidator` / `reconciler` 字段；`NewEmbeddedBinder` 增加 `nodeGetter NodeGetter` 参数（第 6 参数）；`BindUnit()` 绑定前执行节点验证步骤；`Start()` / `Stop()` 管理 reconciler 生命周期；新增 `bindermetrics` 导入 |
| `pkg/binder/embedded_binder_test.go` | 现有 3 处 `NewEmbeddedBinder()` 调用增加第 6 参数 `nil`；新增 3 个 NodeValidator 集成测试 |
| `cmd/scheduler/app/server.go` | 新增 `v1` 导入；通过 Node Informer Lister 构建 `NodeGetter` 并传入 `NewEmbeddedBinder` |

### Phase 4 交付物对照

- [x] **模块 D**：`NodeValidator` — 通过 `GodelSchedulerNodeAnnotationKey` 注解验证节点归属；支持 nil NodeGetter 跳过验证（向后兼容）；**注解缺失/为空时视为未分区节点，允许绑定通过**
- [x] **`NodeOwnershipError`**：类型化错误，支持 `errors.As` 解包；`IsNodeOwnershipError()` 辅助函数
- [x] **模块 J**：`BinderTasksReconciler` 增强 — `NewBinderTaskReconcilerWithRetry` 支持 `maxLocalRetries` 配置；`cleanupPod` 按重试次数选择清理策略；`Len()` 队列长度检查
- [x] **绑定指标**：4 个新增 Prometheus 指标（`node_validation_failures`、`embedded_binder_bind_total`、`embedded_binder_bind_latency`、`dispatcher_fallback_total`）
- [x] **EmbeddedBinder 集成**：`BindUnit()` 在绑定前执行节点验证；验证失败直接返回错误，不进入绑定流程
- [x] **Scheduler 集成**：`server.go` 通过 Node Informer Lister 提供 `NodeGetter`

### 部署调试修复记录

| 问题现象 | 根因 | 修复 |
|---------|------|------|
| kind 集群中所有绑定均被 NodeValidator 拒绝 | kind 节点没有 `godel.bytedance.com/scheduler-name` 注解，原逻辑将空注解视为“不属于本调度器” | `Validate()` 中当 `owner == ""` 时 `return nil`（未分区节点允许绑定）；仅当注解存在且不匹配时才拒绝 |

### 测试结果

- NodeValidator 测试：12 个，全部 PASS
- Reconciler 测试：9 个，全部 PASS
- NodeValidator 集成测试：3 个（OwnedNode / OtherNode / Disabled），全部 PASS
- `pkg/binder/...` 全量测试：全部 PASS（含 `-race` 竞态检测）
- `go vet` 无新增告警
- 编译验证：`cmd/scheduler`、`cmd/binder` 均正常构建

---

## Phase 5：部署与测试

**状态**：✅ 已完成  
**完成日期**：2026-02-27

### 新增文件

| 文件 | 用途 | 测试数 |
|------|------|--------|
| `manifests/overlays/embedded-binder/kustomization.yaml` | Kustomize overlay，引用 base 并应用 Scheduler / Binder patch | — |
| `manifests/overlays/embedded-binder/scheduler-embedded.yaml` | Strategic merge patch：为 Scheduler 添加 `--enable-embedded-binder=true`、`--max-bind-retries=3`、`--bind-timeout=30s`、`--max-local-retries=5`；提升资源限制（cpu: 1, memory: 1Gi）；添加 `binder-mode: embedded` 标签 | — |
| `manifests/overlays/embedded-binder/binder-disabled.yaml` | 将独立 Binder Deployment 副本数缩放为 0 | — |
| `pkg/binder/integration_test.go` | 端到端集成测试（10 个测试） | 10 |
| `pkg/binder/benchmark_test.go` | 性能基准测试（7 个基准） | 7 |
| `cmd/scheduler/app/options/fallback_test.go` | 配置开关回退验证测试 | 5 |

### 修改文件

| 文件 | 改动 |
|------|------|
| `Makefile` | 新增 `local-up-embedded` target，通过 `KUSTOMIZE_PATH` 环境变量指向 `manifests/overlays/embedded-binder` |
| `hack/make-rules/local-up.sh` | 新增 `KUSTOMIZE_PATH` 环境变量支持（默认 `manifests/base`），允许外部覆盙 kustomize 路径 |

### 部署清单

**嵌入模式部署**（使用 Kustomize overlay）：
```bash
kubectl apply -k manifests/overlays/embedded-binder/
```

**回退到独立模式**（使用原始 base）：
```bash
kubectl apply -k manifests/base/
```

Scheduler patch 新增的 CLI 参数：

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--enable-embedded-binder` | `true`（overlay 中） | 启用嵌入式 Binder |
| `--max-bind-retries` | `3` | 单 Pod 最大绑定重试次数 |
| `--bind-timeout` | `30s` | 单次绑定操作超时时间 |
| `--max-local-retries` | `5` | 累计失败次数阈值，超过后回退到 Dispatcher |

### 集成测试（10 个）

| 测试 | 覆盖场景 |
|------|----------|
| `EndToEnd_SinglePodBind` | 单 Pod 完整绑定流程（请求验证 → 绑定 → 结果） |
| `ConfigSwitch_EmbeddedVsStandalone` | 嵌入模式 vs 独立模式配置切换（2 个子测试） |
| `PodGroup_MultiPodBind` | 5 Pod PodGroup 批量绑定 |
| `BindFailure_RetryCountAndFallback` | 绑定失败重试 + 重试计数 + reconciler 队列入队 |
| `NodeReshuffle_ValidationBlocks` | 节点验证阻止非本分区节点绑定 |
| `FunctionalEquivalence` | 嵌入模式 ↔ 独立模式结果等价性验证 |
| `EndToEnd_WithNodeValidation_MultiPod` | 含节点验证的多 Pod 端到端绑定 |
| `ConcurrentMultiScheduler` | 多 Scheduler 并发绑定（race 检测） |
| `Lifecycle_StartStopRestart` | Start/Stop/Restart 生命周期完整性 |
| `Timeout_MultiplePods` | 单请求含多 Pod 超时处理 |

### 基准测试结果（Apple M3 Max）

| 基准 | ns/op | B/op | allocs/op |
|------|-------|------|-----------|
| `SinglePod` | 1,002 | 2,397 | 20 |
| `Parallel` | 1,434 | 3,745 | 28 |
| `PodGroup/5` | 3,610 | 10,237 | 71 |
| `CacheAdapter_AssumePod` | 67 | 24 | 1 |
| `NodeValidator_Validate` | 335 | 1,104 | 3 |
| `WithNodeValidation` | 1,397 | — | — |

**扩展性测试（ScalingN）**：

| Pod 数 | ns/op | 扩展倍率 |
|--------|-------|----------|
| 1 | 1,012 | 1.0× |
| 5 | 4,198 | 4.1× |
| 10 | 6,035 | 6.0× |
| 50 | 31,575 | 31.2× |

结论：绑定延迟随 Pod 数**近似线性增长**，无超线性退化，证明架构可扩展。

### 配置开关回退测试（5 个）

| 测试 | 覆盖场景 |
|------|----------|
| `DisabledByDefault` | 默认 `EnableEmbeddedBinder = false` |
| `EnableAndDisableSwitch` | flag 解析：`=true` / `=false` / 隐式 true（3 个子测试） |
| `ConfigPropagation` | 非默认配置值在禁用时仍正确保留 |
| `DefaultConfigValues` | 默认配置与 `binder.Default*` 常量一致 |
| `ValidationOnlyWhenEnabled` | 启用时校验非法配置；禁用时跳过校验 |

### 修复记录

| 问题 | 修复 |
|------|------|
| `EmbeddedBinder.Start()` 后 `Stop()` 再 `Start()` 导致 `close of closed channel` panic | `Start()` 每次调用时创建新的 reconciler 实例（不再在构造函数中预创建） |
| `makePod` 函数名与 `cache_adapter_test.go` 冲突 | 重命名为 `makeIntegrationPod` |
| `CacheAdapter.AssumePod` 参数类型不匹配 | 改用 `&framework.CachePodInfo{Pod: pod}` 包装 |

### Phase 5 交付物对照

- [x] **部署清单**：Kustomize overlay（3 个文件）支持一键切换嵌入模式 / 独立模式
- [x] **`make local-up-embedded`**：Makefile 新增 target，通过 `KUSTOMIZE_PATH` 环境变量一键部署 embedded binder + Prometheus 监控
- [x] **集成测试**：10 个端到端测试覆盖绑定流程、配置切换、并发、生命周期、超时
- [x] **基准测试**：7 个性能基准 + 扩展性测试，验证近线性扩展
- [x] **配置回退测试**：5 个测试验证 flag 解析、配置传播、条件校验
- [x] **全量回归**：`pkg/binder/...` 全部 PASS（含 `-race` 竞态检测），编译通过

### 测试结果

- 集成测试：10 个，全部 PASS（含 `-race`）
- 基准测试：7 个，全部 PASS
- 配置回退测试：5 个，全部 PASS（含 `-race`）
- `go vet` 无新增告警（pre-existing issues in upstream code only）
- 编译验证：`go build ./cmd/scheduler/...` 通过

---

## Phase 6：Prometheus 指标实现

**状态**：✅ 已完成  
**完成日期**：2026-02-26

### 背景

Phase 4 定义了 4 个指标但仅 `nodeValidationFailures` 有调用点，其余 3 个（`embeddedBinderBindTotal`、`embeddedBinderBindLatency`、`dispatcherFallbackTotal`）从未被接线。为论文基准测试需要完整的可观测性覆盖。

### 新增指标（4 个）

| 指标全名 | 类型 | 标签 | 用途 |
|----------|------|------|------|
| `binder_embedded_bind_pods_total` | Counter | scheduler, result | 单 Pod 绑定成功/失败计数，`rate()` 可得 pods/s 吞吐量 |
| `binder_embedded_bind_pod_duration_seconds` | Histogram | scheduler, result | 单 Pod 绑定耗时（含重试），14 个 bucket |
| `binder_embedded_bind_retries_total` | Counter | scheduler | API 瞬态错误重试计数，反映 API Server 压力 |
| `binder_embedded_bind_inflight` | Gauge | scheduler | 并发执行中的 BindUnit 数量 |

### 已有指标接线修复（3 个）

| 指标全名 | 类型 | 接线位置 |
|----------|------|----------|
| `binder_embedded_bind_total` | Counter | `BindUnit()` 末尾，按 success/failure/partial_failure 分类 |
| `binder_embedded_bind_duration_seconds` | Histogram | `BindUnit()` 末尾，记录端到端 Unit 绑定耗时 |
| `binder_dispatcher_fallback_total` | Counter | `CleanupPodAnnotationsWithRetryCount()` 中 `ShouldDispatchToAnotherScheduler()` 分支 |

### 指标注册修复

| 问题 | 修复 |
|------|------|
| Embedded binder 模式下 `binder/metrics.Register()` 从未被调用 | 在 `cmd/scheduler/app/server.go` 的 embedded binder 初始化块中添加 `bindermetrics.Register()`，将所有 binder 指标注册到全局 `legacyregistry`，通过已有的 `/metrics` 端点暴露 |

### 修改文件

| 文件 | 改动 |
|------|------|
| `pkg/binder/metrics/embedded_binder_metrics.go` | 新增 4 个指标变量定义 + 4 个观测函数（`ObserveEmbeddedBindPod`、`ObserveEmbeddedBindRetry`、`IncEmbeddedBindInflight`、`DecEmbeddedBindInflight`）；`init()` 中注册 8 个指标 |
| `pkg/binder/embedded_binder.go` | `BindUnit()` 中接入 inflight gauge（入口 Inc / defer Dec）、unit 级计时 + `ObserveEmbeddedBind()`、per-pod 计时 + `ObserveEmbeddedBindPod()`；`bindPodToNode()` 重试分支接入 `ObserveEmbeddedBindRetry()` |
| `pkg/binder/utils/util.go` | `CleanupPodAnnotationsWithRetryCount()` 中 fallback 分支接入 `ObserveDispatcherFallback()` |
| `cmd/scheduler/app/server.go` | 新增 `bindermetrics` 导入；embedded binder 初始化块中调用 `bindermetrics.Register()` |

### 指标完整清单（8 个）

| # | 指标名 | 类型 | 接线位置 |
|---|--------|------|----------|
| 1 | `binder_node_validation_failures_total` | Counter | `BindUnit()` 节点校验循环 |
| 2 | `binder_embedded_bind_total` | Counter | `BindUnit()` 末尾 |
| 3 | `binder_embedded_bind_duration_seconds` | Histogram | `BindUnit()` 末尾 |
| 4 | `binder_dispatcher_fallback_total` | Counter | `CleanupPodAnnotationsWithRetryCount()` |
| 5 | `binder_embedded_bind_pods_total` | Counter | `BindUnit()` per-pod 循环 |
| 6 | `binder_embedded_bind_pod_duration_seconds` | Histogram | `BindUnit()` per-pod 循环 |
| 7 | `binder_embedded_bind_retries_total` | Counter | `bindPodToNode()` 重试分支 |
| 8 | `binder_embedded_bind_inflight` | Gauge | `BindUnit()` 入口/出口 |

### 暴露链路

```
init() → AddMetrics(8 个 binder 指标)
       ↓
bindermetrics.Register() → legacyregistry.MustRegister(每个指标)
       ↓
installMetricHandler() → legacyregistry.Handler() → HTTP /metrics 端点
       ↓
Prometheus scrape http://<scheduler-pod>:10251/metrics
```

### 测试结果

- `pkg/binder/...` 全量测试：87 个，全部 PASS
- `go build ./...`：编译通过

---

## Phase 7：Prometheus 监控部署

**状态**：✅ 已完成  
**完成日期**：2026-02-26

### 背景

Phase 6 完成指标实现后，需要在 kind 集群中自动部署 Prometheus 并 scrape 所有 Gödel 组件的 `/metrics` 端点，为论文基准测试提供完整的可观测性基础设施。

### 架构

```
kind cluster (godel-demo-labels)
├── godel-system namespace
│   ├── scheduler (port 10251 /metrics)  ← 含 embedded binder 指标
│   ├── dispatcher (port 10251 /metrics)
│   └── 3 × headless Service → Prometheus endpoint SD 自动发现
└── monitoring namespace
    └── prometheus (NodePort 30090) → scrape 所有 /metrics
```

### 新增文件（6 个）

| 文件 | 用途 |
|------|------|
| `manifests/monitoring/kustomization.yaml` | Kustomize 入口，聚合所有监控资源 |
| `manifests/monitoring/namespace.yaml` | `monitoring` 命名空间 |
| `manifests/monitoring/prometheus-rbac.yaml` | ServiceAccount + ClusterRole（读取 endpoints、pods、/metrics）+ ClusterRoleBinding |
| `manifests/monitoring/prometheus-config.yaml` | Prometheus 配置（3 个 scrape job + 27 条 recording rules） |
| `manifests/monitoring/prometheus-deployment.yaml` | Prometheus Deployment（prom/prometheus:v2.51.0）+ NodePort Service（30090） |
| `manifests/monitoring/godel-services.yaml` | 3 个 headless Service（scheduler / dispatcher / binder），暴露 10251 metrics 端口 |

### 修改文件（5 个）

| 文件 | 改动 |
|------|------|
| `manifests/base/deployment/scheduler.yaml` | 添加 `containerPort: 10251`（metrics） |
| `manifests/base/deployment/dispatcher.yaml` | 添加 `containerPort: 10251`（metrics） |
| `manifests/base/deployment/binder.yaml` | 添加 `containerPort: 10251`（metrics） |
| `manifests/quickstart-feature-examples/godel-demo-labels.yaml` | control-plane 添加 `extraPortMappings: 30090→30090`（Prometheus UI 从 host 可达） |
| `hack/make-rules/local-up.sh` | 新增步骤 4：pull `prom/prometheus:v2.51.0` 镜像 → load 到 kind → `kustomize build monitoring` |

### Prometheus 配置详情

**Scrape Jobs（3 个）**：

| Job | 服务发现 | 目标 Service |
|-----|----------|-------------|
| `godel-scheduler` | `kubernetes_sd_configs` endpoints | `godel-scheduler-metrics` (10251) |
| `godel-dispatcher` | `kubernetes_sd_configs` endpoints | `godel-dispatcher-metrics` (10251) |
| `godel-binder` | `kubernetes_sd_configs` endpoints | `godel-binder-metrics` (10251) |

**Recording Rules（27 条，3 组）**：

#### `embedded_binder` 组（19 条）

| Recording Rule | 含义 |
|----------------|------|
| `godel:binder_embedded_bind_pods:rate1m` | Pod 绑定吞吐量 (pods/s, 1m) |
| `godel:binder_embedded_bind_pods:rate5m` | Pod 绑定吞吐量 (pods/s, 5m) |
| `godel:binder_embedded_bind_units:rate1m` | Unit 绑定吞吐量 (units/s, 1m) |
| `godel:binder_embedded_bind_units:rate5m` | Unit 绑定吞吐量 (units/s, 5m) |
| `godel:binder_embedded_bind_duration:p50` | Unit 绑定延迟 P50 |
| `godel:binder_embedded_bind_duration:p90` | Unit 绑定延迟 P90 |
| `godel:binder_embedded_bind_duration:p99` | Unit 绑定延迟 P99 |
| `godel:binder_embedded_bind_duration:avg` | Unit 绑定平均延迟 |
| `godel:binder_embedded_bind_pod_duration:p50` | 单 Pod 绑定延迟 P50 |
| `godel:binder_embedded_bind_pod_duration:p90` | 单 Pod 绑定延迟 P90 |
| `godel:binder_embedded_bind_pod_duration:p99` | 单 Pod 绑定延迟 P99 |
| `godel:binder_embedded_bind_pod_duration:avg` | 单 Pod 绑定平均延迟 |
| `godel:binder_embedded_bind_pods:success_rate5m` | Pod 绑定成功率 |
| `godel:binder_embedded_bind_units:success_rate5m` | Unit 绑定成功率 |
| `godel:binder_embedded_bind_retries:rate5m` | API 重试速率 |
| `godel:binder_dispatcher_fallback:rate5m` | Dispatcher 回退速率 |
| `godel:binder_node_validation_failures:rate5m` | 节点验证失败速率 |

#### `scheduler_pipeline` 组（6 条）

| Recording Rule | 含义 |
|----------------|------|
| `godel:scheduler_pod_scheduling_attempts:rate1m` | 调度吞吐量 (1m) |
| `godel:scheduler_pod_scheduling_attempts:rate5m` | 调度吞吐量 (5m) |
| `godel:scheduler_e2e_scheduling_duration:p50` | E2E 调度延迟 P50 |
| `godel:scheduler_e2e_scheduling_duration:p90` | E2E 调度延迟 P90 |
| `godel:scheduler_e2e_scheduling_duration:p99` | E2E 调度延迟 P99 |
| `godel:scheduler_e2e_scheduling_duration:avg` | E2E 调度平均延迟 |
| `godel:scheduler_scheduling_algorithm_duration:p90` | 核心算法延迟 P90 |
| `godel:scheduler_scheduling_algorithm_duration:p99` | 核心算法延迟 P99 |

#### `e2e_pipeline` 组（2 条）

| Recording Rule | 含义 |
|----------------|------|
| `godel:e2e_schedule_and_bind_duration:p90_estimate` | 调度+绑定组合 P90 估算 |
| `godel:e2e_schedule_and_bind_duration:p99_estimate` | 调度+绑定组合 P99 估算 |

### 使用方式

```bash
# 一键部署 embedded binder + Prometheus 监控
make local-up-embedded

# Prometheus UI
open http://localhost:30090

# 查询 embedded binder 指标
# 在 Prometheus 输入框中键入 "godel:" 前缀即可自动补全所有预计算指标

# 直接查询原始指标
curl http://localhost:30090/api/v1/query?query=binder_embedded_bind_inflight

# 查询吞吐量
curl http://localhost:30090/api/v1/query?query=godel:binder_embedded_bind_pods:rate5m
```

### Phase 7 交付物对照

- [x] **Prometheus 自动部署**：`local-up.sh` 自动 pull 镜像 + load 到 kind + apply 监控清单
- [x] **服务发现**：3 个 headless Service + Kubernetes endpoint SD，无需硬编码 IP
- [x] **Recording Rules**：27 条预计算规则覆盖吞吐量、延迟百分位、成功率、重试率、回退率
- [x] **端口暴露**：kind `extraPortMappings` 30090 → host，`NodePort` Service
- [x] **向后兼容**：监控栈独立于 Gödel 主清单，现有部署流程无破坏性改动

### 验证结果

- `kustomize build manifests/monitoring`：9 个资源，构建通过
- `kustomize build manifests/overlays/embedded-binder`：15 个资源，构建通过
- `go build ./...`：编译通过
- Prometheus 配置 YAML 语法验证通过

---

## 总结

### 全阶段统计

| 阶段 | 新增文件 | 修改文件 | 新增测试 | 状态 |
|------|----------|----------|----------|------|
| Phase 1：接口定义与配置开关 | 4 | 2 | 24 个子用例 | ✅ |
| Phase 2：核心集成 | 9 | 7 | 59 个函数/用例 | ✅ |
| Phase 3：PodGroupController 迁移 | 2 | 4 | 14 个测试 | ✅ |
| Phase 4：容错与节点验证 | 4 | 4 | 24 个测试 | ✅ |
| Phase 5：部署与测试 | 6 | 2 | 22 个测试 + 7 基准 | ✅ |
| Phase 6：Prometheus 指标实现 | 0 | 4 | — (复用已有测试) | ✅ |
| Phase 7：Prometheus 监控部署 | 6 | 5 | — (基础设施) | ✅ |
| **合计** | **31** | **26** | **143+ 测试 + 7 基准** | ✅ |

### 架构改造核心成果

1. **性能**：单 Pod 绑定延迟 ~1μs，近线性扩展至 50 Pod
2. **可靠性**：节点验证 + 重试 + Dispatcher 回退三级容错
3. **兼容性**：`--enable-embedded-binder` 一键切换，零配置回滚
4. **可观测性**：8 个 Prometheus 指标 + 27 条预计算 Recording Rules + 自动化 Prometheus 部署
5. **部署**：Kustomize overlay 支持声明式切换部署模式；`make local-up-embedded` 一键部署含监控栈
