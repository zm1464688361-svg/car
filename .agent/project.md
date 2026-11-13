# .agent 项目约束：智驾车云-车队日志策略下发平台 log_strategy_fleet

> 对齐 SQL：`log_strategy_fleet.sql`（MySQL 8.0.16+；注意 MySQL 5.7 下 CHECK 约束不生效，业务层必须复现校验）
>
> 技术栈：Golang + Kitex RPC + MySQL + Redis
>
> MySQL 账号：`root`，密码：`asdfghjkl`
>
> 岗位对齐：车云上下行交互模块、策略下发平台、车辆运营平台
>
> 生效范围：本目录 go 代码、IDL、dao、skill、service、handler、单测、SQL、markdown 文档
>
> 业务铁律编号 R-01 ~ R-14；扩展边界规则 R-A01 ~ R-A11；稳定性&租户&运维边界 R-B01 ~ R-B14；
> 补充角色/IDL/车端拉取/时间等高级中级约束 R-C01 ~ R-C12；补充草稿状态、快照、事务、链路追踪、软删除等边界约束 R-D01 ~ R-D10。
>
> 编码必须遵守，纯规则收敛至 skill 包。
>
> 补充：已确认需要在 `log_strategy_activity` 增加 JSON 字段 `activity_baseline_snapshot`，存储活动创建时刻车辆基线快照，见 SQL 变更片段。

---

## 名词统一定义（必读，消除歧义）

- **未结束活动**：activity 处于 `DRAFT` / `APPROVED` / `PROCESSING` / `STOPPED` / `RECOVERING`，`active_flag=1`；`COMPLETED`/`RECOVER_COMPLETED` 为已结束，`active_flag=NULL`。
- **原始轮次**：`round_no=1`，申请试点推广下发的业务轮次；每个 activity 仅有 1 个原始轮次。
- **恢复轮次**：`round_no=2`，**复用同一个 activity_no 主记录，不新建 log_strategy_activity 行**；仅新增 `log_activity_vehicle_item` 明细行。
- **曾经下发车辆**：原始轮次中 `distribute_status = DISTRIBUTED` 的车辆集合（`stopped_distributed_vins` 快照）；只有这批车辆需要执行恢复。
- **SKIPPED 车辆**：停止时未下发车辆，`distribute_status=SKIPPED`，从未下发配置，不需要生成恢复轮次明细。
- **纯 Skill 函数**：仅接收内存入参结构体，**不导入 dao、不访问数据库/redis**，可直接单元测试；负责参数、枚举、版本、业务条件判断。
- **数据依赖校验**：需要先查询 DB 获取数据，再传给纯 Skill 做判断；这部分查询逻辑写在 service 层，禁止放到 skill 包内部。
- **审计流水优先**：尽可能保留回执原始记录；即使业务更新失败，审计记录尽量不丢失；实现见 R-14 补充。

---

## 数据库 Schema 契约（严格遵循 log_strategy_fleet.sql，禁止私自改表字段语义）

### 数据表清单

1. `veh_vehicle_base_config` 车辆基线配置表：`base_log_interval` 为恢复基线；`last_effect_interval` 仅合法回执更新。
2. `log_strategy_activity` 活动主表；`uk_tenant_fleet_active` 保证同租户+车队同时最多 1 个未结束活动；`active_flag=1` 占位。
   - **【SQL 需新增字段】** `activity_baseline_snapshot` JSON NOT NULL，COMMENT '活动创建时刻车辆基线快照，vin->base_log_interval，用于恢复轮次计算，申请提交时生成写入，后续只读不修改'。
3. `log_activity_vehicle_item` 逐车明细表【核心核查表】；`round_no=1` 原始下发 / `round_no=2` 恢复轮次；`uk_activity_vin_round` 唯一键。
4. `log_vehicle_ack_record` 回执审计流水：所有回执必先落此表；`rule_check_result` 标记校验结果；`applied_flag` 标记是否回写业务。
5. `log_approval_operate_log` 审批&操作流水，全操作留痕。
6. `sys_dict` 系统字典；状态枚举全部取自字典，禁止硬编码字面量。
7. 视图：`v_log_activity_vehicle_check` 逐车核查视图，对外报表优先使用此视图。

### 重要字段语义强制约束

- `expect_interval`：平台下发期望值；distribute 成功只写此字段，**绝不修改 `actual_effect_interval`**。
- `actual_effect_interval`：实际生效值，仅 `rule_check_result=PASS` 的 SUCCESS 回执才更新；FAIL/TIMEOUT/PENDING 不改动此字段。
- `vehicle_version`：单车版本号，单车全局单调递增；回执 `report_version` 必须严格匹配本字段。
- `timeout_time`：首次判定超时时间，迟到成功更新 `ack_status=SUCCESS`，**timeout_time 必须保留不清除**。
- `distribute_status=SKIPPED`：活动停止后未下发车辆置 SKIPPED，不再下发；已下发车辆状态不动。
- `round_no=2` 恢复轮次：恢复是全新轮次，不能修改 `round_no=1` 旧行；`before_interval` 取自原始轮次 `actual_effect_interval`；`expect_interval` 取自本活动 `activity_baseline_snapshot` 快照，不再实时查询 `veh_vehicle_base_config`。
- **soft-delete**：全部表使用 `is_deleted` 软删除，禁止物理 DELETE 业务数据；回执流水、操作流水只 INSERT 不删除。

---

## 业务铁律 R-01 ~ R-14（必须固化 skill 包，禁止散写业务代码 if-else）

- **R-01 下发不等于生效**：distribute（下发）成功仅写 `expect_interval`；`actual_effect_interval` 唯一可信来源为校验通过的 SUCCESS 回执。
- **R-02 版本强匹配**：回执 `report_version != vehicle_version` 直接拒绝；旧版本（`report_version < vehicle_version`）标记 `REJECT_OLD_VERSION`，不更新业务状态；未知版本（`report_version > vehicle_version`）标记 `REJECT_UNKNOWN_VERSION`。
- **R-03 回执鉴权**：vin 必须匹配当前明细行 vin；跨车回执标记 `REJECT_CROSS_VIN` 直接拒绝，只落审计流水不改动业务。
- **R-04 同车队互斥**：`uk_tenant_fleet_active` 数据库唯一索引兜底；上层 skill 同样校验同租户+车队不能并行存在多个未结束活动。
  - 补充：
    1. `active_flag=1` 仅用于【未结束活动】；流转到 `COMPLETED` / `RECOVER_COMPLETED` 必须设置 `active_flag=NULL` 释放车队占位；
    2. 恢复轮次 `round_no=2` 属于同一个 activity，不修改 `active_flag`，不新建主活动记录；
    3. skill 只是前置拦截；业务代码必须捕获 MySQL 唯一索引冲突异常，返回对应业务 biz_code，不能完全依赖 skill 防止并发创建。
- **R-05 审批人不能等于申请人**：数据库 CHECK 约束兜底；skill 层同样校验。
- **R-06 试点强制门槛**：`pilot_vin_list` 固定 2 台；必须两台 PILOT 批次 `ack_status=SUCCESS`（`pilot_check_status=PASSED`），才允许执行 rollout 推广；任意 FAIL/TIMEOUT 则禁止推广，活动进入已暂停。
- **R-07 停止语义**：停止仅把未下发车辆 `distribute_status` 置 SKIPPED；**不修改任何 `actual_effect_interval`；不会自动发起恢复**；`stopped_distributed_vins` / `stopped_pending_vins` 快照落库留存。
- **R-08 恢复语义**：恢复为独立 `round_no=2` 轮次，新增 `log_activity_vehicle_item` 行，不修改原始 `round_no=1` 记录；`expect_interval` 取自活动主表 `activity_baseline_snapshot` 基线快照；恢复也要完整申请审批流程。
  - 补充：
    1. 恢复明细仅为【曾经下发车辆】，SKIPPED 车辆不生成 `round_no=2` 明细；
    2. 恢复复用原有 activity_no，更新主表 recover_* 字段，禁止新建 `log_strategy_activity` 主记录；
    3. 无论原始活动最终状态是 STOPPED 或者 COMPLETED，均支持发起恢复；
    4. 恢复下发失败可重复执行 DistributeRecover，复用 `round_no=2`，禁止生成 `round_no>=3`。
- **R-09 活动完成判定**：`round_no=1` 全部目标车辆 `ack_status=SUCCESS`，才可标记 COMPLETED；存在 PENDING/FAIL/TIMEOUT 禁止标记完成。
- **R-10 恢复完成判定**：`round_no=2` 所有 RECOVER 批次车辆 `ack_status=SUCCESS` 才可标记 RECOVER_COMPLETED；存在未确认项禁止标记恢复完成。
- **R-11 超时规则**：`distribute_time + 60s` 后无 PASS 回执置 `ack_status=TIMEOUT`，写入 `timeout_time`；后续收到合法 SUCCESS 回执更新 `ack_status=SUCCESS`，但 `timeout_time` 保留历史痕迹。
  - 补充：
    1. 超时状态**禁止查询时内存动态计算**，必须由【异步定时任务】扫描持久化更新 DB 字段 `timeout_time` 与 `ack_status`；
    2. 核查视图 `v_log_activity_vehicle_check` 直接读取数据库持久化的超时字段。
- **R-12 FAIL 回执处理**：FAIL 回执只写 `fail_reason_code` / `fail_reason_msg`；**`actual_effect_interval` 保持原值，禁止回退到基线**。
- **R-13 回执幂等**：同一 `ack_no` 重复上报标记 `REJECT_DUPLICATE`，不重复更新 `ack_count` 与业务状态；Redis 做回执幂等拦截。
- **R-14 回执审计优先**：无论回执合法/非法，必须先写入 `log_vehicle_ack_record` 审计流水，再执行业务更新；拒绝的回执 `applied_flag=0`，不修改业务明细表。
  - 补充：
    1. 不要将回执 insert 审计与业务 update 放在同一个 MySQL 事务，会造成业务异常时审计记录被回滚丢失；
    2. 两阶段处理：阶段 1 独立小事务插入回执审计记录；阶段 2 开启事务执行业务校验更新明细；
    3. 增加补偿任务：扫描 `process_time` 为空的回执审计记录，补执行业务处理，防止服务 crash 导致回执未处理。

---

## 扩展边界规则 R-A01 ~ R-A11（高风险边界约束）

- **R-A01 Redis 分布式锁粒度与使用边界**
  1. 写操作：`SubmitApply`/`ApproveActivity`/`DistributePilot`/`RolloutVehicle`/`StopActivity`/`SubmitRecover`/`ApproveRecover`/`DistributeRecover`/`MarkComplete`，锁 key=`log_strategy:act:{activity_no}`；
  2. 回执上报 `ReportVehicleAck` **不加活动分布式锁**；依靠 MySQL 行锁（where activity_no + vin + round_no）做并发写保护；
  3. 分布式锁过期时间必须大于业务 DB 事务最大执行时间；业务禁止长事务；
  4. Redis 锁仅做冲突削减，不能替代数据库唯一索引、行锁约束。
- **R-A02 STOPPED 状态下回执行为**
  1. 活动 STOPPED 后，`distribute_status=DISTRIBUTED` 车辆收到合法匹配版本回执：允许更新 ack 状态，标记 `ACCEPT_LATE_SUCCESS`；
  2. STOPPED 状态下合法回执处理完成，**不会自动触发推广、不会自动流转活动状态**；
  3. STOPPED 状态下 SKIPPED 车辆上报回执直接拒绝 `REJECT_UNKNOWN_VERSION`，写入审计流水。
- **R-A03 下发操作原子性，防止服务 crash 产生中间态**：试点下发、推广下发、恢复下发，整批车辆状态变更必须放在同一个 MySQL 事务；保证整批全部更新 `distribute_status`，或者全部回滚；禁止部分成功部分失败中间状态。
- **R-A04 接口幂等约束**：所有写 RPC 接口必须增加入参 `request_id string`（调用方生成）；使用 Redis 缓存 `request_id` 实现请求幂等，避免重复提交申请、重复审批、重复下发。
- **R-A05 sys_dict 字典加载与参数校验**
  1. 服务启动一次性全量加载 `sys_dict` 字典到内存；运行期不每次查询 DB；字典变更需要重启/主动刷新缓存；
  2. 所有外部 RPC 传入状态字符串，必须校验是否存在内存字典集合；非法枚举值直接返回参数错误，禁止入库。
- **R-A06 Skill 分层约束**
  1. 纯 Skill 函数：只接收内存结构体，无 dao/redis 依赖，便于单元测试；负责参数校验、版本比对、业务条件判断；
  2. 凡是需要查询数据库才能拿到判断条件的逻辑，放在 service 层：service 先查询 DB 得到数据，再传入纯 skill 函数做判断；
  3. 严格禁止 skill 包内部导入 dao 直接访问数据库。
- **R-A07 异步定时任务约束（必须实现两个后台 Job 组件，独立进程，不和 RPC/HTTP 请求链路混在一起）**
  - **Task-1 超时判定任务**：周期扫描 `distribute_status='DISTRIBUTED' AND ack_status='PENDING' AND distribute_time + 60s < now()`；更新 `ack_status=TIMEOUT`、填充 `timeout_time`；
  - **Task-2 回执补偿任务**：周期扫描 `log_vehicle_ack_record where process_time IS NULL`；执行回执业务处理，防止进程 crash 导致回执只落审计未执行业务更新。
- **R-A08 活动状态流转保护**：不允许绕过状态机直接修改 `activity_status`；所有状态变更必须经过 skill 状态流转校验。
- **R-A09 试点判定 pilot_check_status 更新时机**：只有两台 PILOT 批次明细 `ack_status` 都更新完成后，才允许更新 `pilot_check_status` 为 PASSED/FAILED/TIMEOUT；禁止业务代码手动硬编码赋值。
- **R-A10 恢复轮次约束**：恢复下发时，如果部分车辆 FAIL/TIMEOUT，允许重复调用 DistributeRecover 重试，复用 `round_no=2` 明细记录，不新增行。
- **R-A11 数据库 CHECK 约束仅做兜底**：MySQL CHECK 约束在 5.7 版本不生效，所有 CHECK 对应的业务校验逻辑必须在 skill 层实现，不能只依赖数据库约束。

---

## 补充稳定性 & 租户 & 运维边界规则 R-B01 ~ R-B14

- **R-B01 租户强隔离约束**
  1. dao 层所有 select/update/delete 操作，**必须携带 tenant_id 条件，禁止省略 tenant_id 做全租户查询**；
  2. RPC 入参 tenant_id 不允许为空，skill 前置校验非空；
  3. 禁止上层业务通过传入非法 tenant_id 跨租户访问数据。
- **R-B02 定时任务分布式运行约束（Task-1 超时判定、Task-2 回执补偿）【详见 R-A01 Redis 锁】**
  1. 定时任务启动前必须抢占 Redis 分布式锁 key=`log_strategy:task:{taskName}`，锁超时大于单次任务最大执行时间；防止多实例重复执行；
  2. 任务分批分页扫描 DB，限制单次 batch 大小，禁止一次性全表扫描；
  3. Task-2 回执补偿任务设置单条回执最大重试次数；
     - 备注：死信标记逻辑暂时内存/日志识别，**不新增 DB 字段，不修改原始 DDL**；达到最大重试次数停止重试，输出 error 日志标记死信，等待人工排查，不自动丢弃原始审计记录；
  4. 定时任务连续执行失败达到阈值输出 error 告警日志；
  5. 定时任务禁止修改原始轮次 `round_no=1` 业务记录之外的其他无关业务数据。
- **R-B03 车辆基线快照约束**
  1. 申请提交阶段读取 `veh_vehicle_base_config`，生成快照写入 `log_strategy_activity.activity_baseline_snapshot`，活动全生命周期读取此快照；不再实时查询 `veh_vehicle_base_config`；
  2. 恢复轮次 `expect_interval` 直接取自 `activity_baseline_snapshot`。
- **R-B04 人工运维数据修复约束**
  1. 业务应用代码禁止程序逻辑直接 UPDATE 回执流水表 `log_vehicle_ack_record` 的已有历史行；
  2. 人工 DBA 补录异常回执场景允许操作；**优先 INSERT 新增回执行；尽量不 UPDATE 已有历史回执记录；必须完整保留全部原始审计记录，不可覆盖删除原有数据**；
  3. 确需人工修正业务数据，必须同步插入一条 `log_approval_operate_log` 运维操作流水，记录操作人、修改内容；
  4. 禁止手工直接修改 `activity_status`、`vehicle_version`、`timeout_time` 字段，必须走服务层状态流转接口。
- **R-B05 Kitex 客户端重试约束**
  1. `ReportVehicleAck` 回执上报 RPC，客户端层面**关闭 kitex 自动重试**；回执重试逻辑由业务层控制；
  2. 写业务接口依靠 `request_id` 做幂等，允许合理重试，禁止无限制重试。
- **R-B06 业务埋点指标与告警要求**
  1. 【代码强制】必须埋点 metrics：回执各 `rule_check_result` 计数、试点失败计数、活动 STOPPED 计数、定时任务执行耗时/失败数、补偿任务死信计数；
  2. 文档参考（不在业务代码实现）：告警触发场景：大量 `REJECT_CROSS_VIN` 跨车回执、大量 `REJECT_OLD_VERSION` 旧版本回执、回执补偿任务堆积死信、定时任务连续多次执行失败；告警规则在监控平台配置，业务代码不写阈值判断逻辑。
- **R-B07 数据生命周期归档约束**：文档参考，不作为编码强制约束；归档由 DBA/binlog 脚本实现，业务 Go 代码不实现归档逻辑；`log_vehicle_ack_record`、`log_approval_operate_log` 审计流水，保留业务约定周期，到期执行归档；已完成/已停止的历史活动支持归档。
- **R-B08 车辆元数据缺失处理**：当 `log_activity_vehicle_item` 明细存在，但 `veh_vehicle_base_config` 查询不到对应 vin，标记车辆记录异常，输出 error 日志告警，不可 panic 崩溃；不继续执行下发、恢复逻辑。
- **R-B09 已完成活动写保护**：状态为 COMPLETED / RECOVER_COMPLETED 的活动，禁止再执行：下发试点、推广、停止、发起恢复等写操作，仅允许查询核查接口。
- **R-B10 Redis 降级约束【详见 R-A01 Redis 锁】**
  1. 写操作（申请、审批、下发、停止、恢复）依赖 Redis 分布式锁/幂等 key，Redis 不可用时写接口直接返回业务失败，禁止穿透写数据库；
  2. `ReportVehicleAck` 回执上报链路**不依赖 Redis**，只依靠 MySQL 行锁并发保护；Redis 故障回执上报链路保持可用。
- **R-B11 RPC 入参长度校验**：所有字符串入参，必须按照数据库字段最大长度做校验，超长直接返回参数错误；禁止数据库发生字符串静默截断。
- **R-B12 JSON 字段操作约束**：`log_strategy_activity` 内部 `pilot_vin_list` / `rollout_vin_list` / `target_vin_list` / `activity_baseline_snapshot` 等 JSON 字段，禁止直接字符串切割拼接；必须完整反序列化为内存结构体修改，再序列化回存 DB；防止 JSON 损坏。
- **R-B13 ack_count 计数规则**：`log_activity_vehicle_item.ack_count` 仅统计 `rule_check_result=PASS` 的 SUCCESS 有效回执；REJECT 类非法回执不增加 `ack_count`。

---

## 补充角色、版本、IDL、车端拉取、时间时区高级 & 中级边界 R-C01 ~ R-C12

- **R-C01 vehicle_version 版本递增强约束**
  1. `vehicle_version` 为 `log_activity_vehicle_item` 单车明细行维度版本；仅在执行下发试点、下发推广、下发恢复业务动作时执行 +1；
  2. 审批、回执上报、查询、停止活动**禁止修改 vehicle_version**；
  3. 同一明细行（activity_no+vin+round_no）版本号只允许单调向上；禁止回退、跳减版本号。
- **R-C02 IDL 字段非空约束**
  1. kitex-thrift IDL 中，业务核心入参 `request_id`、`tenant_id`、`activity_no`、`vin`、`round_no`、`ack_no` 必须定义为 required，不能为 optional；
  2. 禁止核心业务 ID 字段生成 Go 指针 nil 类型，避免空指针 panic。
- **R-C03 活动快照 vin 强校验**：所有下发、回执处理流程，传入 vin 必须存在于当前活动 `target_vin_list` 快照集合；不在快照集合内的 vin 直接拒绝，写入审计/返回参数错误。
- **R-C04 round_no 取值范围校验**：`round_no` 业务仅允许取值 1（原始下发）、2（恢复轮次）；RPC 入参必须经过 skill 校验；`round_no` 不在 {1,2} 直接返回参数错误，禁止入库。
- **R-C05 JSON 写入合法性校验**：写入 DB 的 JSON 字段 `activity_baseline_snapshot` / `pilot_vin_list` / `rollout_vin_list` / `target_vin_list`，写入前必须校验可以正常序列化、非损坏 JSON；拒绝写入非法 JSON 字符串入库。
- **R-C06 车端配置拉取接口约束（业务原始需求：车辆认证后拉取配置）**
  1. 车端拉取配置为只读 RPC 接口，不修改任何数据库状态；
  2. 必须做车辆身份鉴权；
  3. 返回该车对应活动下最新 `vehicle_version`、`expect_interval`；
  4. 拉取接口不会生成回执，回执只能通过 `ReportVehicleAck` 上报。
- **R-C07 业务 biz_code 编码约束**
  1. 业务错误 biz_code 分类：参数错误、角色权限错误、状态流转非法、并发冲突、回执校验拒绝、系统内部异常；
  2. 同一个业务失败场景必须返回固定 biz_code，不允许随机变更错误码。
- **R-C08 操作角色权限校验**（skill 层增加操作者角色校验）
  1. 仅研发角色允许提交申请；
  2. 仅审批负责人角色允许审批、停止活动；
  3. 仅运营角色允许下发试点、推广、下发恢复；
  4. 角色身份不匹配直接返回权限错误，禁止进入后续业务逻辑。
- **R-C09 时间统一约束**：全链路时间统一使用 UTC 时区；数据库 datetime 存储 UTC 时间；所有时间差、超时计算全部基于 UTC，禁止混用本地时区时间。
- **R-C10 空记录防护约束**：DB 查询不到业务记录，返回结构化业务 error，禁止直接对 nil 结构体进行字段访问，禁止 panic。
- **R-C11 业务单号解析约束**：`activity_no`、`ack_no` 仅作为唯一标识；禁止业务代码解析字符串文本提取业务信息；所有业务属性必须读取数据库字段。
- **R-C12 第三方依赖约束**：kitex、mysql 驱动、redis 客户端使用正式稳定 release 版本，不使用 alpha/beta 开发测试版；废弃 kitex 旧 API 不允许使用。

---

## 补充草稿状态、快照、事务、链路追踪、软删除等边界约束 R-D01 ~ R-D10

- **R-D01 草稿状态参数修改约束**
  1. 活动状态 DRAFT 草稿：允许修改申请原因、`target_interval`、`target_vin_list`、`pilot_vin_list`；
  2. 一旦提交审批，状态流转到待审批（DRAFT 之外），**`target_interval`、`target_vin_list`、`pilot_vin_list`、`rollout_vin_list` 禁止再修改**；审批通过后同样禁止原地修改。
- **R-D02 停止快照 stopped_distributed_vins、stopped_pending_vins 约束**
  1. StopActivity 执行停止活动时，在同一个停止事务内原子写入 `stopped_distributed_vins`、`stopped_pending_vins`；
  2. 快照写入完成后，两个字段全程只读，业务代码禁止后续再次修改；恢复逻辑读取该快照判断哪些车辆需要生成 `round_no=2` 明细。
- **R-D03 恢复轮次 before_interval 赋值强制校验**：`round_no=2` 恢复轮次的 `log_activity_vehicle_item.before_interval`，必须取自该 vin 在 `round_no=1` 明细行的 `actual_effect_interval`；skill 层做校验，不允许直接读取基线快照赋值 `before_interval`。
- **R-D04 草稿状态操作保护**：DRAFT 草稿状态，禁止执行下发试点、推广、停止活动、发起恢复等写业务动作；仅允许修改草稿、提交审批。
- **R-D05 MySQL 批量事务大小约束**：下发推广、恢复等批量更新 DB 操作，单事务内处理记录数设置上限，禁止超大事务，避免长 binlog、锁等待。
- **R-D06 RPC 限流约束**：所有对外 RPC 接口（`SubmitApply`、`ReportVehicleAck` 等）必须接入 kitex 限流中间件，保护数据库，防止流量突打垮存储。
- **R-D07 后台 Job 链路 trace_id 约束**：Task-1、Task-2 后台定时 Job 生成独立 trace_id；日志必须打印 trace_id，便于问题链路排查；和 RPC 日志字段保持一致。
- **R-D08 软删除 is_deleted 查询强制条件**：dao 层所有 select 业务查询，必须带上条件 `is_deleted = 0`；禁止业务逻辑读取 `is_deleted=1` 软删除记录参与业务计算。
- **R-D09 数据库查询规范【文档参考，不驱动代码生成】**：业务查询禁止使用 `select *`；核查报表大结果集必须分页；禁止不带索引全表扫描；该约束由评审/DBA 检查。
- **R-D10 vehicle_version 数值防护**：`vehicle_version` 使用 int64；下发递增版本时做溢出判断，溢出返回业务错误，禁止版本号溢出回绕。

---

## Go + Kitex 编码强制约束

1. 遵循 Go 官方规范，Kitex 微服务开发规范；包名小写短名；禁止全局变量保存业务状态；状态持久化 MySQL；Redis 用于分布式锁、回执幂等防重、热点缓存【详见 R-A01】。
2. 分层严格：kitex-handler → service → skill（校验层）→ dao；**所有写操作先调用 skill 校验，校验失败直接返回业务错误，禁止进入 dao 层写库**。
3. IDL 优先：所有 RPC 接口先写 `idl/log_strategy_fleet.thrift`，kitex 生成代码；禁止手写 RPC 结构体；trace_id 全链路透传；所有写接口增加 `request_id` 幂等入参【详见 R-A04】。
4. 错误处理：禁止 `_` 吞 error；业务错误使用 kitex 自定义 biz_code；skill 校验返回结构化业务错误；dao 层错误向上透传；捕获数据库唯一索引冲突异常返回业务错误。
5. 并发安全：活动申请、下发、停止、恢复操作获取 Redis 分布式锁；回执上报依赖 MySQL 行锁；禁止依赖本地 sync.Mutex 做集群并发保护【详见 R-A01】。
6. 枚举全部映射 `sys_dict` 内存缓存；代码内使用字符串常量，禁止裸写魔法字符串；外部传入枚举值做合法性校验【详见 R-A05】；状态流转全部 skill 校验。
7. DB 操作：下发整批变更使用 MySQL 事务；回执采用两阶段处理【详见 R-14 补充】；禁止长事务；回执审计优先保证尽可能留存。
8. JSON/DB tag：model 结构体必须同时具备 json、db 标签；时间优先使用 datetime，Go 侧 `time.Time`；业务唯一业务单号 `activity_no`、`ack_no` 由服务端生成 uuid/snowflake，不依赖前端传入。
9. 入参校验放在函数最开头，非法参数直接返回错误，不执行业务逻辑。
10. skill 包要求：区分【纯 Skill 无 DB 依赖】与 service 层数据查询逻辑；纯 skill 每个校验点独立函数，全部可单元测试；业务规则不允许出现在 handler/service/dao。

---

## Redis 使用约束

- 用途：1）分布式锁；2）回执幂等 key：`ack_no`；3）写接口幂等 key：`request_id`；4）热点缓存：活动主信息、车辆版本；**禁止 Redis 存储权威业务状态，权威状态以 MySQL 为准**。
- 缓存策略：读缓存，写直接落 MySQL；写后主动失效缓存；不做持久业务数据存放；分布式锁设置合理过期防死锁，过期大于事务最大耗时。
- 回执上报 RPC 不拿活动分布式锁，依靠 MySQL 行锁做并发保护【详见 R-A01】。

---

## RPC 接口与入出参约束

1. 回执上报 RPC：`ReportVehicleAck`，入参 `ack_no, activity_no, vin, round_no, report_version, report_result, report_interval`；前置 skill 全量回执校验；非法回执落审计返回业务错误，不修改业务状态【详见 R-14】。
2. 核查查询 RPC：`QueryActivityVehicleCheck`，输出字段必须包含：`before_interval`、`expect_interval`、`actual_effect_interval`、`vehicle_version`、`distribute_status`、`ack_status`、`timeout_time`、`fail_reason_code`、确认状态；**禁止只返回活动总状态，必须逐车明细输出**。
3. 车端只读 RPC：`PullVehicleConfig`；鉴权通过返回该车最新 `vehicle_version`、`expect_interval`；只读，不修改 DB【详见 R-C06】。
4. 所有写接口：`SubmitApply` / `ApproveActivity` / `DistributePilot` / `RolloutVehicle` / `StopActivity` / `SubmitRecover` / `ApproveRecover` / `DistributeRecover` / `MarkComplete` / `MarkRecoverComplete`；全部先走 skill 校验；全部携带 `request_id` 幂等入参【详见 R-A04】。
5. 返回体统一：`code, biz_code, msg, data`；biz_code 业务错误码 LC1001-LC5006 系列。

---

## 日志约束（zap 结构化日志）

1. 必打日志字段：`trace_id, activity_no, vin, round_no, vehicle_version, operator`。
2. 必打印点：申请提交、审批通过/驳回、试点下发、推广下发、停止活动、恢复申请、恢复下发、回执上报、回执拒绝、超时判定、标记完成/恢复完成；**异步定时任务执行异常必须打 error 日志**；Job 任务独立生成并打印 trace_id【详见 R-D07】。
3. 回执拒绝必须打 warn 日志，输出 `rule_check_result`、`rule_check_msg`；用于排查跨车、旧版本、重复回执问题；死信达到最大重试次数输出 error 日志标记死信。
4. 禁止打印明文数据库密码；conf 配置敏感信息 gitignore，不提交代码仓库。

---

## 单元测试强制约束

1. skill 包全部纯校验函数必须单测；使用 testify；skill 单测不依赖数据库，传入入参直接断言返回结果。
2. 必须覆盖全部 5 大业务场景 case：
   - **场景 A**：正常流程：申请-审批-试点双成功-推广-全部成功-标记完成。
   - **场景 B**：审批驳回分支：提交-驳回-修改重提审批。
   - **场景 C**：试点失败/超时：试点单台超时-自动暂停-停止活动，剩余车辆 SKIPPED 不下发。
   - **场景 D**：恢复分支：部分车辆生效-停止-恢复申请审批-下发恢复轮次；存在未确认车辆禁止标记恢复完成。
   - **场景 E**：网络异常：迟到成功回执保留超时记录；旧版本回执拒绝；跨车回执拒绝；重复回执幂等；FAIL 回执不回退 `actual_effect_interval`。
3. 补充必须覆盖边界 case：
   - 活动 STOPPED 状态接收迟到合法回执；
   - 写接口 `request_id` 幂等重复调用；
   - 外部传入非法字典枚举值拦截；
   - 租户隔离校验；
   - 回执补偿任务最大重试次数、日志死信标记；
   - 已完成活动写保护拦截；
   - `vehicle_version` 仅下发动作递增，其它操作不修改版本；
   - `round_no` 非法入参拦截；
   - vin 不在 `target_vin_list` 快照集合拦截；
   - 操作者角色权限校验拦截；
   - `activity_baseline_snapshot` 快照只读；
   - DRAFT 提交审批后禁止修改活动参数；
   - StopActivity 停止快照字段写入后只读；
   - 恢复轮次 `before_interval` 取值正确性；
   - DRAFT 草稿禁止下发/停止；
   - dao 查询强制携带 `is_deleted=0`；
   - UTC 时间时区一致性校验；
   - `vehicle_version` 版本递增溢出判断。
4. 重点断言：`actual_effect_interval` 不会被非法回执修改；`timeout_time` 不被迟到成功清除；`round_no=2` 恢复不改动 `round_no=1` 原始记录；SKIPPED 车辆不会被下发；下发事务原子性；`activity_baseline_snapshot` 快照只读。

---

## 项目目录强制结构

```
log_strategy_fleet/
├── .agent/                    # 项目约束文档（本文件）
├── idl/                       # kitex thrift IDL，写接口必须先更新 idl，写接口增加 request_id 入参
├── sql/
│   └── log_strategy_fleet.sql # 唯一 DDL 源，已增加 activity_baseline_snapshot 字段，禁止代码私自偏离 schema
├── conf/                      # yaml 配置，mysql/redis 配置，敏感信息 gitignore
├── internal/
│   ├── model/                 # DB model、RPC model、常量、字典内存映射
│   ├── skill/                 # R-01~R-14 全部纯业务校验，无 db 依赖
│   ├── service/               # 业务编排，获取锁、查询 DB、事务、调用 skill+dao；数据依赖校验放此处
│   ├── handler/               # kitex rpc handler，参数解析，透传 trace
│   └── dao/                   # mysql、redis 读写层，禁止业务逻辑写 dao
├── job/                       # 独立后台定时 Job 组件；Task-1 超时判定、Task-2 回执补偿实现于此，不和 kitex 主进程 handler 混编
├── test/                      # 业务场景集成测试、dao 层集成测试
├── cmd/demo                   # 模拟业务场景 demo 脚本
└── docs/                      # 接口文档、错误码、业务流程图、边界 case 说明文档、运维归档 & 监控参考文档
```

---

## 禁止清单（红线，编码禁止写出下面逻辑）

- ❌ 下发 distribute 成功直接修改 `actual_effect_interval`。
- ❌ 旧版本回执覆盖新高版本 `actual_effect_interval`。
- ❌ 试点未全部成功就放行 rollout 推广。
- ❌ StopActivity 停止时自动修改 `actual_effect_interval`，自动下发恢复配置。
- ❌ 回执校验失败直接静默丢弃，不落 `log_vehicle_ack_record` 审计流水。
- ❌ `round_no=2` 恢复轮次直接修改 `round_no=1` 原始明细表行。
- ❌ 存在未确认车辆，代码允许标记 COMPLETED / RECOVER_COMPLETED。
- ❌ 同一租户+车队允许并行多个未结束活动。
- ❌ FAIL/TIMEOUT 回执把 `actual_effect_interval` 回退至基线。
- ❌ 删除回执流水、操作流水，清除 `timeout_time` 历史超时痕迹。
- ❌ 业务规则写在 handler/service/dao，不收敛到 skill 包。
- ❌ 集群环境依靠本地 sync.Mutex 做集群并发保护，不用 Redis 分布式锁。
- ❌ 魔法字符串硬编码状态，不使用 `sys_dict` 内存常量映射；不校验外部传入枚举。
- ❌ 查询核查接口只返回活动总状态，不输出逐车明细。
- ❌ 查询接口内存动态计算超时状态，不依赖定时任务持久化 `timeout_time`。
- ❌ skill 包内部 import dao 执行数据库查询。
- ❌ 将回执审计 insert 和业务 update 写在同一个事务，审计记录随业务异常回滚丢失。
- ❌ STOPPED 活动直接丢弃合法迟到回执。
- ❌ 恢复流程新建 `log_strategy_activity` 主记录。
- ❌ 下发操作不使用事务，允许部分车辆下发成功部分失败中间态。
- ❌ 写接口不实现幂等，缺少 `request_id`，允许重复审批、重复下发。
- ❌ dao 层写/查省略 `tenant_id` 条件，跨租户查询数据。
- ❌ 定时任务不加分布式锁，多实例并发重复更新数据库。
- ❌ `ReportVehicleAck` 开启 kitex 底层自动重试。
- ❌ 业务直接字符串拼接/切割处理数据库 JSON 字段。
- ❌ 对 COMPLETED / RECOVER_COMPLETED 活动执行下发、停止、恢复写操作。
- ❌ Redis 不可用，写业务穿透绕过 Redis 锁直接写数据库。
- ❌ 回执补偿任务无限循环重试坏回执，无最大重试终止条件。
- ❌ 业务应用代码 UPDATE 修改已有历史回执流水行。
- ❌ 人工绕过服务层直接 SQL 修改 `vehicle_version`、`timeout_time`、`activity_status`。
- ❌ 在审批/回执/查询/停止逻辑修改 `vehicle_version` 版本号。
- ❌ IDL 核心业务 ID 字段定义为 optional，生成 nil 指针。
- ❌ 处理下发/回执时放行不在 `target_vin_list` 快照中的 vin。
- ❌ `round_no` 传入非 1、2 数值直接放行入库。
- ❌ 写入非法损坏 JSON 对象到数据库 JSON 字段。
- ❌ 车端 `PullVehicleConfig` 接口修改业务 DB 数据。
- ❌ 不同业务场景返回不固定 biz_code。
- ❌ 绕过 skill 角色校验直接执行业务写操作。
- ❌ 时间计算混用本地时区，不全链路使用 UTC。
- ❌ 直接访问 nil 结构体字段不返回业务 error。
- ❌ 解析 `activity_no`/`ack_no` 字符串提取业务逻辑。
- ❌ 提交审批之后修改 `target_interval` / `target_vin_list` / `pilot_vin_list` / `rollout_vin_list`。
- ❌ StopActivity 完成后修改 `stopped_distributed_vins` / `stopped_pending_vins` 快照字段。
- ❌ 恢复 `round_no=2` 轮次 `before_interval` 直接取基线快照，不从 `round_no=1` `actual_effect_interval` 读取。
- ❌ DRAFT 草稿状态执行下发试点、推广、停止、发起恢复。
- ❌ dao 业务查询不携带 `is_deleted=0`，读取软删除记录参与业务计算。
- ❌ `vehicle_version` 版本递增不做溢出判断，发生数值回绕。

---

## AI 生成输出约束

1. 生成 Go 代码优先输出完整分层包，model → skill → service → handler → dao 顺序；优先写 skill 校验，再写业务逻辑。
2. 生成 IDL、SQL、接口文档，必须对齐 `log_strategy_fleet.sql` 字段，包含新增 `activity_baseline_snapshot`；写接口务必带上 `request_id` 幂等参数；核心 ID 字段 IDL 定义为 required；增加 `PullVehicleConfig` 车端拉取接口。
3. 单测使用 testify，case 命名清晰，每个 case 注释对应 R-xx / R-Axx / R-Bxx / R-Cxx / R-Dxx 铁律编号。
4. 核查报表输出格式对齐 `v_log_activity_vehicle_check` 视图输出字段。
5. 新增代码先输出 skill 校验逻辑，再输出 service 业务逻辑。
6. 定时任务代码生成放在 `job/` 包，禁止写进 kitex handler/service；必须实现分布式锁、分批扫描、最大重试次数、日志死信标记；**不新增死信 DB 字段**；Job 生成独立 trace_id 打印日志。
7. metrics 仅做埋点输出，不在业务代码编写告警阈值判断逻辑。
8. dao 查询必须带上 `is_deleted=0` 条件；skill 层增加角色权限校验逻辑；`vehicle_version` 递增增加溢出判断。
