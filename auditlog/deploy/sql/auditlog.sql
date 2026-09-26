-- auditlog 服务 MySQL 建库建表脚本
-- 执行方式：mysql -uroot -p < deploy/sql/auditlog.sql

CREATE DATABASE IF NOT EXISTS `auditlog`
    DEFAULT CHARACTER SET utf8mb4
    DEFAULT COLLATE utf8mb4_unicode_ci;

USE `auditlog`;

CREATE TABLE IF NOT EXISTS `audit_log` (
    `id`            CHAR(36)     NOT NULL COMMENT '日志主键，UUIDv7',
    `trace_id`      VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '链路追踪 ID',
    `service_name`  VARCHAR(128) NOT NULL DEFAULT '' COMMENT '产生日志的服务名',
    `operation`     VARCHAR(256) NOT NULL DEFAULT '' COMMENT '具体操作（方法/接口路径）',
    `actor_id`      VARCHAR(128) NOT NULL DEFAULT '' COMMENT '操作者 ID',
    `actor_type`    VARCHAR(32)  NOT NULL DEFAULT '' COMMENT '操作者类型：user/service/admin',
    `action`        VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '动作类型：CREATE/UPDATE/DELETE/LOGIN/QUERY 等',
    `resource_type` VARCHAR(128) NOT NULL DEFAULT '' COMMENT '资源类型',
    `resource_id`   VARCHAR(128) NOT NULL DEFAULT '' COMMENT '资源 ID',
    `source_ip`     VARCHAR(64)  NOT NULL DEFAULT '' COMMENT '来源 IP',
    `user_agent`    VARCHAR(512) NOT NULL DEFAULT '' COMMENT 'User-Agent',
    `request_uri`   VARCHAR(512) NOT NULL DEFAULT '' COMMENT '请求 URI',
    `status_code`   INT          NOT NULL DEFAULT 0 COMMENT '业务状态码',
    `request_body`  MEDIUMTEXT   NULL COMMENT '请求体（需脱敏后存储）',
    `response_body` MEDIUMTEXT   NULL COMMENT '响应体（需脱敏后存储）',
    `metadata`      JSON         NULL COMMENT '扩展元数据',
    `error_message` VARCHAR(2048) NOT NULL DEFAULT '' COMMENT '错误信息',
    `duration_ms`   INT UNSIGNED NOT NULL DEFAULT 0 COMMENT '耗时（毫秒）',
    `created_at`    DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) COMMENT '创建时间，毫秒精度',
    PRIMARY KEY (`id`),
    KEY `idx_service_created` (`service_name`, `created_at`),
    KEY `idx_actor_created`   (`actor_id`, `created_at`),
    KEY `idx_resource`        (`resource_type`, `resource_id`),
    KEY `idx_action_created`  (`action`, `created_at`),
    KEY `idx_trace_id`        (`trace_id`),
    KEY `idx_created_at`      (`created_at`)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4
  COLLATE = utf8mb4_unicode_ci
  COMMENT = '审计日志表';
