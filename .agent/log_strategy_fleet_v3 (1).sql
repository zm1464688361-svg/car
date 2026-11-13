-- =============================================================
-- 文件名：log_strategy_fleet_v3.sql
-- 用途 ：智驾车云 · 测试车队日志策略的申请、审批、试点发布、推广、停止与恢复
-- 数据库：MySQL 8.0.16+（CHECK 约束需 8.0.16+；5.7 可执行但 CHECK 不生效，
--         因此所有 CHECK 对应规则必须在 Go 的 skill 校验层复现）
-- 字符集：utf8mb4 / utf8mb4_unicode_ci
-- 版本变更：v3 在 log_strategy_activity 新增 activity_baseline_snapshot（活动基线快照）
-- 导入：
--   mysql -uroot -p'asdfghjkl' -e "CREATE DATABASE IF NOT EXISTS log_strategy
--     DEFAULT CHARSET utf8mb4 COLLATE utf8mb4_unicode_ci;"
--   mysql -uroot -p'asdfghjkl' log_strategy < log_strategy_fleet_v3.sql
-- =============================================================

SET NAMES utf8mb4;
SET FOREIGN_KEY_CHECKS = 0;

-- -------------------------------------------------------------
-- 1. 车辆基线配置表 veh_vehicle_base_config
--    车辆当前态汇总；恢复动作的目标值来源（base_log_interval）
-- -------------------------------------------------------------
DROP TABLE IF EXISTS `veh_vehicle_base_config`;
CREATE TABLE `veh_vehicle_base_config` (
  `id` bigint NOT NULL AUTO_INCREMENT COMMENT '内部主键',
  `tenant_id` varchar(32) NOT NULL COMMENT '租户ID',
  `fleet_code` varchar(32) NOT NULL COMMENT '车队编码',
  `vin` varchar(32) NOT NULL COMMENT '车辆VIN，车辆唯一标识',
  `base_log_interval` int NOT NULL DEFAULT 60 COMMENT '活动前基线间隔(秒)，初始60，恢复配置的目标值',
  `last_effect_interval` int NOT NULL DEFAULT 60 COMMENT '车辆当前实际生效的日志间隔(秒)，只由有效回执更新',
  `last_effect_version` bigint NOT NULL DEFAULT 0 COMMENT '车辆最近一次生效的版本号',
  `last_published_version` bigint NOT NULL DEFAULT 0 COMMENT '平台最近一次向该车发布的版本号，车端拉取配置时以此为准',
  `last_activity_no` varchar(64) DEFAULT NULL COMMENT '最近一次生效所属活动单号，溯源用',
  `creator` varchar(32) NOT NULL COMMENT '创建人账号',
  `updater` varchar(32) NOT NULL COMMENT '更新人账号',
  `create_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间（UTC 存储）',
  `update_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间（UTC 存储）',
  `is_deleted` tinyint NOT NULL DEFAULT 0 COMMENT '软删除：0-未删除，1-已删除',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_tenant_fleet_vin` (`tenant_id`,`fleet_code`,`vin`),
  KEY `idx_tenant_vin` (`tenant_id`,`vin`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='车辆基线配置表';

-- -------------------------------------------------------------
-- 2. 日志策略发布活动主表 log_strategy_activity
--    一次申请=一张活动单，覆盖 申请→审批→试点→推广→停止→恢复 全周期
--    active_flag：进行中=1，已结束=NULL（利用唯一索引允许多个 NULL）
--    【v3 新增】activity_baseline_snapshot：申请提交时写入的基线快照，全周期只读
-- -------------------------------------------------------------
DROP TABLE IF EXISTS `log_strategy_activity`;
CREATE TABLE `log_strategy_activity` (
  `id` bigint NOT NULL AUTO_INCREMENT COMMENT '内部主键',
  `activity_no` varchar(64) NOT NULL COMMENT '活动业务单号，对外唯一单据号',
  `tenant_id` varchar(32) NOT NULL COMMENT '租户ID',
  `fleet_code` varchar(32) NOT NULL COMMENT '目标车队编码',
  `config_type` varchar(32) NOT NULL DEFAULT 'LOG_INTERVAL' COMMENT '配置类型：LOG_INTERVAL-日志上报间隔（非安全类配置）',
  `apply_reason` text NOT NULL COMMENT '申请原因（排查问题描述）',
  `target_interval` int NOT NULL COMMENT '申请的新日志间隔(秒)，范围[10,300]，批准后不可原地修改',
  `ack_timeout_seconds` int NOT NULL DEFAULT 60 COMMENT '回执超时阈值(秒)，默认60，超时即判定TIMEOUT',
  `applicant` varchar(32) NOT NULL COMMENT '申请人账号（研发）',
  `approver` varchar(32) NOT NULL COMMENT '审批人账号（负责人），不得与申请人相同',
  `activity_status` varchar(32) NOT NULL COMMENT '活动状态：DRAFT/REJECTED/APPROVED/PROCESSING/STOPPED/COMPLETED/RECOVERING/RECOVER_COMPLETED',
  `active_flag` tinyint DEFAULT NULL COMMENT '进行中占位：1-未结束(占位)，NULL-已结束(不占位)；配合唯一索引保证同车队单活动',
  `current_round` int NOT NULL DEFAULT 1 COMMENT '当前发布轮次：1-原始下发，2-恢复轮次；禁止出现>=3',
  `pilot_check_status` varchar(16) NOT NULL DEFAULT 'PENDING' COMMENT '试点判定：PENDING-未判定 / PASSED-两台均成功 / FAILED-存在失败 / TIMEOUT-存在超时；非PASSED不得推广',
  `pilot_vin_list` json NOT NULL COMMENT '试点车辆VIN数组（固定2台），批准后不可变更',
  `rollout_vin_list` json NOT NULL COMMENT '推广批次VIN数组（剩余3台）',
  `target_vin_list` json NOT NULL COMMENT '本活动全部目标车辆VIN数组（5台），申请时快照，防范围漂移',
  `activity_baseline_snapshot` json NOT NULL COMMENT '活动创建时刻车辆基线快照，vin->base_log_interval；申请提交时生成写入，后续只读不修改，恢复轮次 expect_interval 由此读取',
  `apply_time` datetime DEFAULT NULL COMMENT '提交申请时间',
  `submit_count` int NOT NULL DEFAULT 0 COMMENT '提交次数，退回修改后重新提交时递增',
  `approve_time` datetime DEFAULT NULL COMMENT '审批通过时间',
  `approve_comment` varchar(512) DEFAULT NULL COMMENT '审批通过意见',
  `reject_time` datetime DEFAULT NULL COMMENT '审批退回时间',
  `reject_comment` varchar(512) DEFAULT NULL COMMENT '退回意见',
  `pilot_distribute_time` datetime DEFAULT NULL COMMENT '运营执行试点下发时间',
  `rollout_time` datetime DEFAULT NULL COMMENT '运营确认推广时间（试点PASSED后）',
  `stop_time` datetime DEFAULT NULL COMMENT '活动停止时间',
  `stop_comment` varchar(512) DEFAULT NULL COMMENT '停止原因说明',
  `stopped_distributed_vins` json DEFAULT NULL COMMENT '停止时已下发范围的VIN快照，停止不抹除；写入后只读',
  `stopped_pending_vins` json DEFAULT NULL COMMENT '停止时未下发范围的VIN快照（转SKIPPED）；写入后只读',
  `complete_time` datetime DEFAULT NULL COMMENT '活动完成时间（全部目标车辆确认成功）',
  `recover_applicant` varchar(32) DEFAULT NULL COMMENT '恢复申请人账号',
  `recover_approver` varchar(32) DEFAULT NULL COMMENT '恢复审批人账号',
  `recover_apply_time` datetime DEFAULT NULL COMMENT '恢复申请提交时间',
  `recover_approve_time` datetime DEFAULT NULL COMMENT '恢复审批通过时间',
  `recover_complete_time` datetime DEFAULT NULL COMMENT '恢复完成时间（需恢复车辆全部确认）',
  `creator` varchar(32) NOT NULL COMMENT '创建人',
  `updater` varchar(32) NOT NULL COMMENT '更新人',
  `create_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间（UTC 存储）',
  `update_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间（UTC 存储）',
  `is_deleted` tinyint NOT NULL DEFAULT 0 COMMENT '软删除：0-未删除，1-已删除',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_activity_no` (`activity_no`),
  UNIQUE KEY `uk_tenant_fleet_active` (`tenant_id`,`fleet_code`,`active_flag`),
  KEY `idx_tenant_status` (`tenant_id`,`activity_status`),
  KEY `idx_fleet_code` (`fleet_code`),
  CONSTRAINT `chk_interval_range` CHECK (`target_interval` between 10 and 300),
  CONSTRAINT `chk_approver_not_applicant` CHECK (`approver` <> `applicant`),
  CONSTRAINT `chk_ack_timeout` CHECK (`ack_timeout_seconds` > 0),
  CONSTRAINT `chk_current_round` CHECK (`current_round` in (1,2))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='日志策略发布活动主表';

-- -------------------------------------------------------------
-- 3. 活动车辆明细表 log_activity_vehicle_item 【逐车核查核心表】
--    一车一轮次一行：round_no=1 原始下发，round_no=2 恢复下发
--    期望值/生效值/版本/回执状态/失败原因全部区分保存
-- -------------------------------------------------------------
DROP TABLE IF EXISTS `log_activity_vehicle_item`;
CREATE TABLE `log_activity_vehicle_item` (
  `id` bigint NOT NULL AUTO_INCREMENT COMMENT '内部主键',
  `activity_no` varchar(64) NOT NULL COMMENT '关联活动业务单号',
  `tenant_id` varchar(32) NOT NULL COMMENT '租户ID',
  `fleet_code` varchar(32) NOT NULL COMMENT '车队编码',
  `vin` varchar(32) NOT NULL COMMENT '车辆VIN，必须属于活动 target_vin_list 快照',
  `round_no` int NOT NULL DEFAULT 1 COMMENT '发布轮次：1-原始下发，2-恢复轮次；同车不同轮次分行保存',
  `batch_type` varchar(16) NOT NULL COMMENT '批次类型：PILOT-试点 / ROLLOUT-推广 / RECOVER-恢复',
  `before_interval` int NOT NULL COMMENT '本轮发布前值(秒)：核查对比与恢复回退依据；恢复轮次取自第1轮 actual_effect_interval',
  `expect_interval` int NOT NULL COMMENT '期望值(秒)：本轮希望该车生效的间隔',
  `actual_effect_interval` int DEFAULT NULL COMMENT '实际生效值(秒)：仅合法SUCCESS回执写入；失败/超时不覆盖旧值；NULL=从未生效',
  `vehicle_version` bigint NOT NULL COMMENT '车辆维度版本号，单车跨活动跨轮次单调递增；回执版本必须与之精确匹配',
  `source_version` bigint DEFAULT NULL COMMENT '恢复轮次对应的原始下发版本号，溯源用',
  `distribute_status` varchar(32) NOT NULL DEFAULT 'PENDING' COMMENT '下发状态：PENDING-待下发 / DISTRIBUTED-已下发(不等于生效) / SKIPPED-跳过（停止后不再下发）',
  `distribute_time` datetime DEFAULT NULL COMMENT '运营执行下发动作时间（UTC）',
  `ack_status` varchar(32) NOT NULL DEFAULT 'PENDING' COMMENT '回执状态：PENDING-待回执 / SUCCESS-生效成功 / FAIL-应用失败 / TIMEOUT-超时',
  `ack_time` datetime DEFAULT NULL COMMENT '最近一次有效回执时间；超时后迟到成功也写此字段',
  `timeout_time` datetime DEFAULT NULL COMMENT '首次判定超时的时间点；非空即代表曾超时，迟到成功仍保留',
  `ack_count` int NOT NULL DEFAULT 0 COMMENT '有效回执次数（仅 PASS 且 SUCCESS 的计数）；重复成功不重复记账',
  `last_ack_no` varchar(64) DEFAULT NULL COMMENT '最近一次有效回执流水号，关联回执流水表',
  `fail_reason_code` varchar(64) DEFAULT NULL COMMENT '失败原因编码，对应字典 log_fail_reason',
  `fail_reason_msg` varchar(512) DEFAULT NULL COMMENT '失败原因补充描述（说明文案由字典 code 翻译）',
  `creator` varchar(32) NOT NULL COMMENT '创建人',
  `updater` varchar(32) NOT NULL COMMENT '更新人',
  `create_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间（UTC 存储）',
  `update_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间（UTC 存储）',
  `is_deleted` tinyint NOT NULL DEFAULT 0 COMMENT '软删除：0-未删除，1-已删除',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_activity_vin_round` (`activity_no`,`vin`,`round_no`),
  KEY `idx_activity_round_batch` (`activity_no`,`round_no`,`batch_type`),
  KEY `idx_activity_ack` (`activity_no`,`ack_status`),
  KEY `idx_tenant_vin` (`tenant_id`,`vin`),
  KEY `idx_distribute_ack` (`distribute_status`,`ack_status`,`distribute_time`),
  CONSTRAINT `chk_expect_interval` CHECK (`expect_interval` between 10 and 300),
  CONSTRAINT `chk_round_no` CHECK (`round_no` in (1,2))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='活动车辆明细表（逐车核查核心表）';

-- -------------------------------------------------------------
-- 4. 回执上报审计流水表 log_vehicle_ack_record
--    所有回执（含被拒）先落此表；非法回执只留痕、不改业务明细
-- -------------------------------------------------------------
DROP TABLE IF EXISTS `log_vehicle_ack_record`;
CREATE TABLE `log_vehicle_ack_record` (
  `id` bigint NOT NULL AUTO_INCREMENT COMMENT '内部主键',
  `ack_no` varchar(64) NOT NULL COMMENT '回执流水号，重复回执幂等去重依据',
  `tenant_id` varchar(32) NOT NULL COMMENT '租户ID',
  `activity_no` varchar(64) NOT NULL COMMENT '归属活动单号',
  `vin` varchar(32) NOT NULL COMMENT '上报车辆VIN（鉴权身份）',
  `expect_vin` varchar(32) DEFAULT NULL COMMENT '该版本应回执的车辆VIN，跨车比对用',
  `round_no` int NOT NULL DEFAULT 1 COMMENT '归属发布轮次',
  `report_version` bigint NOT NULL COMMENT '车端上报携带的版本号',
  `report_interval` int NOT NULL COMMENT '车端上报的当前生效间隔(秒)',
  `report_result` varchar(16) NOT NULL COMMENT '车端上报原始结果：SUCCESS / FAIL',
  `device_report_time` datetime DEFAULT NULL COMMENT '车端本地生成回执时间，用于判定迟到回执',
  `rule_check_result` varchar(32) NOT NULL COMMENT '校验结果：PASS / REJECT_AUTH / REJECT_CROSS_VIN / REJECT_OLD_VERSION / REJECT_UNKNOWN_VERSION / REJECT_DUPLICATE / ACCEPT_LATE_SUCCESS',
  `rule_check_msg` varchar(512) DEFAULT NULL COMMENT '校验结果说明',
  `applied_flag` tinyint NOT NULL DEFAULT 0 COMMENT '是否已回写业务明细：0-未回写（被拒绝），1-已回写',
  `receive_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '平台接收回执时间',
  `process_time` datetime DEFAULT NULL COMMENT '规则处理完成时间；为 NULL 表示待补偿任务处理',
  `is_deleted` tinyint NOT NULL DEFAULT 0 COMMENT '软删除：0-未删除，1-已删除',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_ack_no` (`ack_no`),
  KEY `idx_activity_vin_round` (`activity_no`,`vin`,`round_no`),
  KEY `idx_receive_time` (`receive_time`),
  KEY `idx_process_time` (`process_time`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='车辆回执上报审计流水表';

-- -------------------------------------------------------------
-- 5. 审批与操作流水表 log_approval_operate_log
-- -------------------------------------------------------------
DROP TABLE IF EXISTS `log_approval_operate_log`;
CREATE TABLE `log_approval_operate_log` (
  `id` bigint NOT NULL AUTO_INCREMENT COMMENT '内部主键',
  `tenant_id` varchar(32) NOT NULL COMMENT '租户ID',
  `activity_no` varchar(64) NOT NULL COMMENT '关联活动单号',
  `round_no` int NOT NULL DEFAULT 1 COMMENT '操作所属轮次',
  `operate_type` varchar(32) NOT NULL COMMENT '操作类型：SUBMIT_APPLY/APPROVE_PASS/APPROVE_REJECT/DISTRIBUTE_PILOT/ROLLOUT/APPROVE_STOP/SUBMIT_RECOVER/APPROVE_RECOVER/DISTRIBUTE_RECOVER/COMPLETE/RECOVER_COMPLETE',
  `operator` varchar(32) NOT NULL COMMENT '操作人账号',
  `operate_comment` varchar(1024) DEFAULT NULL COMMENT '操作备注',
  `operate_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '操作时间（UTC）',
  `is_deleted` tinyint NOT NULL DEFAULT 0 COMMENT '软删除：0-未删除，1-已删除',
  PRIMARY KEY (`id`),
  KEY `idx_activity_no` (`activity_no`),
  KEY `idx_operator` (`operator`),
  KEY `idx_operate_time` (`operate_time`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='审批与操作流水表';

-- -------------------------------------------------------------
-- 6. 系统字典表 sys_dict（公司已有同名表时，跳过本节 DROP/CREATE，仅执行下文字典 INSERT）
-- -------------------------------------------------------------
DROP TABLE IF EXISTS `sys_dict`;
CREATE TABLE `sys_dict` (
  `id` bigint NOT NULL AUTO_INCREMENT COMMENT '主键',
  `dict_type` varchar(64) NOT NULL COMMENT '字典类型',
  `dict_label` varchar(128) NOT NULL COMMENT '展示名称',
  `dict_value` varchar(128) NOT NULL COMMENT '存储值',
  `sort_no` int NOT NULL DEFAULT 0 COMMENT '排序号',
  `remark` varchar(255) DEFAULT NULL COMMENT '备注',
  `creator` varchar(32) NOT NULL COMMENT '创建人',
  `updater` varchar(32) NOT NULL COMMENT '更新人',
  `create_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  `update_time` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  `is_deleted` tinyint NOT NULL DEFAULT 0 COMMENT '软删除：0-未删除，1-已删除',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_dict_type_value` (`dict_type`,`dict_value`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='系统字典表';

SET FOREIGN_KEY_CHECKS = 1;

-- =============================================================
-- 初始化数据：字典（服务启动时加载进内存，运行期不查 DB）
-- =============================================================
INSERT INTO `sys_dict` (`dict_type`,`dict_label`,`dict_value`,`sort_no`,`remark`,`creator`,`updater`) VALUES
('log_activity_status','草稿','DRAFT',1,'活动状态','admin','admin'),
('log_activity_status','已退回','REJECTED',2,'活动状态','admin','admin'),
('log_activity_status','已批准待发布','APPROVED',3,'活动状态','admin','admin'),
('log_activity_status','发布中','PROCESSING',4,'活动状态','admin','admin'),
('log_activity_status','已停止','STOPPED',5,'活动状态','admin','admin'),
('log_activity_status','已完成','COMPLETED',6,'活动状态','admin','admin'),
('log_activity_status','恢复中','RECOVERING',7,'活动状态','admin','admin'),
('log_activity_status','恢复完成','RECOVER_COMPLETED',8,'活动状态','admin','admin');

INSERT INTO `sys_dict` (`dict_type`,`dict_label`,`dict_value`,`sort_no`,`remark`,`creator`,`updater`) VALUES
('log_batch_type','试点','PILOT',1,'批次类型','admin','admin'),
('log_batch_type','推广','ROLLOUT',2,'批次类型','admin','admin'),
('log_batch_type','恢复','RECOVER',3,'批次类型','admin','admin');

INSERT INTO `sys_dict` (`dict_type`,`dict_label`,`dict_value`,`sort_no`,`remark`,`creator`,`updater`) VALUES
('log_distribute_status','待下发','PENDING',1,'下发状态','admin','admin'),
('log_distribute_status','已下发','DISTRIBUTED',2,'下发状态','admin','admin'),
('log_distribute_status','跳过','SKIPPED',3,'下发状态','admin','admin');

INSERT INTO `sys_dict` (`dict_type`,`dict_label`,`dict_value`,`sort_no`,`remark`,`creator`,`updater`) VALUES
('log_ack_status','待回执','PENDING',1,'回执状态','admin','admin'),
('log_ack_status','生效成功','SUCCESS',2,'回执状态','admin','admin'),
('log_ack_status','应用失败','FAIL',3,'回执状态','admin','admin'),
('log_ack_status','超时','TIMEOUT',4,'回执状态','admin','admin');

INSERT INTO `sys_dict` (`dict_type`,`dict_label`,`dict_value`,`sort_no`,`remark`,`creator`,`updater`) VALUES
('log_pilot_check_status','未判定','PENDING',1,'试点判定','admin','admin'),
('log_pilot_check_status','试点通过','PASSED',2,'试点判定','admin','admin'),
('log_pilot_check_status','试点存在失败','FAILED',3,'试点判定','admin','admin'),
('log_pilot_check_status','试点存在超时','TIMEOUT',4,'试点判定','admin','admin');

INSERT INTO `sys_dict` (`dict_type`,`dict_label`,`dict_value`,`sort_no`,`remark`,`creator`,`updater`) VALUES
('log_ack_rule_check','校验通过','PASS',1,'回执校验','admin','admin'),
('log_ack_rule_check','鉴权失败','REJECT_AUTH',2,'回执校验','admin','admin'),
('log_ack_rule_check','跨车非法回执','REJECT_CROSS_VIN',3,'回执校验','admin','admin'),
('log_ack_rule_check','旧版本回执','REJECT_OLD_VERSION',4,'回执校验','admin','admin'),
('log_ack_rule_check','未知版本','REJECT_UNKNOWN_VERSION',5,'回执校验','admin','admin'),
('log_ack_rule_check','重复回执','REJECT_DUPLICATE',6,'回执校验','admin','admin'),
('log_ack_rule_check','超时后迟到成功','ACCEPT_LATE_SUCCESS',7,'回执校验','admin','admin');

INSERT INTO `sys_dict` (`dict_type`,`dict_label`,`dict_value`,`sort_no`,`remark`,`creator`,`updater`) VALUES
('log_fail_reason','配置应用失败','CONFIG_APPLY_FAIL',1,'失败原因','admin','admin'),
('log_fail_reason','车辆离线未拉取','NETWORK_OFFLINE',2,'失败原因','admin','admin'),
('log_fail_reason','回执超时未回','ACK_TIMEOUT',3,'失败原因','admin','admin'),
('log_fail_reason','回执版本不匹配','VERSION_MISMATCH',4,'失败原因','admin','admin'),
('log_fail_reason','跨车非法回执','CROSS_VIN_ACK',5,'失败原因','admin','admin'),
('log_fail_reason','回执鉴权失败','AUTH_FAIL',6,'失败原因','admin','admin');

-- =============================================================
-- 初始化数据：5 台测试车辆（租户 TEST01，车队 FLEET001，初始 60 秒）
-- =============================================================
INSERT INTO `veh_vehicle_base_config`
(`tenant_id`,`fleet_code`,`vin`,`base_log_interval`,`last_effect_interval`,`last_effect_version`,`last_published_version`,`creator`,`updater`) VALUES
('TEST01','FLEET001','VIN001',60,60,0,0,'admin','admin'),
('TEST01','FLEET001','VIN002',60,60,0,0,'admin','admin'),
('TEST01','FLEET001','VIN003',60,60,0,0,'admin','admin'),
('TEST01','FLEET001','VIN004',60,60,0,0,'admin','admin'),
('TEST01','FLEET001','VIN005',60,60,0,0,'admin','admin');

-- =============================================================
-- 逐车核查视图：一次查询看全 发布前值 / 期望值 / 生效值 / 成功失败 / 未确认
-- =============================================================
DROP VIEW IF EXISTS `v_log_activity_vehicle_check`;
CREATE VIEW `v_log_activity_vehicle_check` AS
SELECT
  item.activity_no                                                    AS `活动单号`,
  item.round_no                                                       AS `轮次`,
  item.fleet_code                                                     AS `车队编码`,
  item.vin                                                            AS `车辆VIN`,
  item.batch_type                                                     AS `批次类型`,
  item.before_interval                                                AS `发布前值秒`,
  item.expect_interval                                                AS `期望值秒`,
  item.actual_effect_interval                                         AS `实际生效值秒`,
  item.vehicle_version                                                AS `下发版本`,
  item.distribute_status                                              AS `下发状态`,
  item.ack_status                                                     AS `回执状态`,
  CASE WHEN item.timeout_time IS NULL THEN '否' ELSE '是' END          AS `是否曾超时`,
  item.ack_count                                                      AS `有效回执次数`,
  item.fail_reason_code                                               AS `失败原因编码`,
  item.fail_reason_msg                                                AS `失败原因`,
  CASE
    WHEN item.ack_status = 'SUCCESS'          THEN '已确认'
    WHEN item.distribute_status = 'SKIPPED'   THEN '未下发'
    ELSE '未确认'
  END                                                                 AS `确认状态`,
  item.distribute_time                                                AS `下发时间`,
  item.ack_time                                                       AS `回执时间`,
  item.timeout_time                                                   AS `超时判定时间`
FROM `log_activity_vehicle_item` item
WHERE item.is_deleted = 0;

-- =============================================================
-- 使用示例（复制到查询窗口，替换活动单号）
-- =============================================================
/*
-- 1) 逐车核查（禁止只看活动总状态）
SELECT * FROM v_log_activity_vehicle_check
WHERE `活动单号` = 'LOG202611130001'
ORDER BY `轮次`, `批次类型`, `车辆VIN`;

-- 2) 未确认项统计（>0 时不得展示“全部完成/全部恢复”）
SELECT activity_no, round_no,
       SUM(CASE WHEN ack_status <> 'SUCCESS' THEN 1 ELSE 0 END) AS unconfirmed_count
FROM log_activity_vehicle_item
WHERE is_deleted = 0
GROUP BY activity_no, round_no;

-- 3) 试点是否满足推广条件（必须两台均为 SUCCESS）
SELECT COUNT(*) AS pilot_success_count
FROM log_activity_vehicle_item
WHERE activity_no = 'LOG202611130001'
  AND round_no = 1
  AND batch_type = 'PILOT'
  AND ack_status = 'SUCCESS'
  AND is_deleted = 0;

-- 4) 待超时扫描的明细（供 Task-1 使用）
SELECT activity_no, vin, round_no, distribute_time
FROM log_activity_vehicle_item
WHERE is_deleted = 0
  AND distribute_status = 'DISTRIBUTED'
  AND ack_status = 'PENDING'
  AND distribute_time < DATE_SUB(UTC_TIMESTAMP(), INTERVAL 60 SECOND);

-- 5) 待补偿的回执（供 Task-2 使用）
SELECT ack_no, activity_no, vin, round_no
FROM log_vehicle_ack_record
WHERE is_deleted = 0
  AND process_time IS NULL
ORDER BY receive_time
LIMIT 200;

-- 6) 恢复完成校验（存在未确认项不得标记恢复完成）
SELECT COUNT(*) AS recover_unconfirmed
FROM log_activity_vehicle_item
WHERE activity_no = 'LOG202611130001'
  AND round_no = 2
  AND ack_status <> 'SUCCESS'
  AND is_deleted = 0;
*/

-- （注：内容由AI生成，导入前请确认目标库与租户/车队编码）
-- （注：内容由AI生成）
