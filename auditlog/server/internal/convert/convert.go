// Package convert 只做协议类型与 biz 领域类型之间的同名字段拷贝；
// 业务规则与语义换算（NULL 解包、时间戳）都在 biz，这里不承载任何判定逻辑。
package convert

import (
	pb "cari.com.cn/framework/auditlog/rpc/auditlog"
	"cari.com.cn/framework/auditlog/server/internal/biz"
	"cari.com.cn/framework/auditlog/server/internal/types"
)

// ---------------- 输入方向：协议请求 → biz 参数 ----------------

// CreateLogFromHTTP 将 HTTP 写入条目转换为领域写入参数。
func CreateLogFromHTTP(item *types.AuditLogItem) biz.CreateLog {
	return biz.CreateLog{
		Id:           item.Id,
		TraceId:      item.TraceId,
		ServiceName:  item.ServiceName,
		Operation:    item.Operation,
		ActorId:      item.ActorId,
		ActorType:    item.ActorType,
		Action:       item.Action,
		ResourceType: item.ResourceType,
		ResourceId:   item.ResourceId,
		SourceIp:     item.SourceIp,
		UserAgent:    item.UserAgent,
		RequestUri:   item.RequestUri,
		StatusCode:   item.StatusCode,
		RequestBody:  item.RequestBody,
		ResponseBody: item.ResponseBody,
		Metadata:     item.Metadata,
		ErrorMessage: item.ErrorMessage,
		DurationMs:   item.DurationMs,
	}
}

// CreateLogsFromHTTP 批量转换 HTTP 写入条目。
func CreateLogsFromHTTP(items []types.AuditLogItem) []biz.CreateLog {
	logs := make([]biz.CreateLog, 0, len(items))
	for i := range items {
		logs = append(logs, CreateLogFromHTTP(&items[i]))
	}
	return logs
}

// CreateLogFromRPC 将 gRPC 写入条目转换为领域写入参数。
func CreateLogFromRPC(item *pb.AuditLog) biz.CreateLog {
	return biz.CreateLog{
		Id:           item.GetId(),
		TraceId:      item.GetTraceId(),
		ServiceName:  item.GetServiceName(),
		Operation:    item.GetOperation(),
		ActorId:      item.GetActorId(),
		ActorType:    item.GetActorType(),
		Action:       item.GetAction(),
		ResourceType: item.GetResourceType(),
		ResourceId:   item.GetResourceId(),
		SourceIp:     item.GetSourceIp(),
		UserAgent:    item.GetUserAgent(),
		RequestUri:   item.GetRequestUri(),
		StatusCode:   item.GetStatusCode(),
		RequestBody:  item.GetRequestBody(),
		ResponseBody: item.GetResponseBody(),
		Metadata:     item.GetMetadata(),
		ErrorMessage: item.GetErrorMessage(),
		DurationMs:   item.GetDurationMs(),
	}
}

// CreateLogsFromRPC 批量转换 gRPC 写入条目。
func CreateLogsFromRPC(items []*pb.AuditLog) []biz.CreateLog {
	logs := make([]biz.CreateLog, 0, len(items))
	for _, item := range items {
		logs = append(logs, CreateLogFromRPC(item))
	}
	return logs
}

// SearchFilterFromHTTP 将 HTTP 查询请求转换为领域查询条件。
func SearchFilterFromHTTP(req *types.SearchAuditLogsRequest) biz.SearchFilter {
	return biz.SearchFilter{
		ServiceName:  req.ServiceName,
		ActorId:      req.ActorId,
		Action:       req.Action,
		ResourceType: req.ResourceType,
		ResourceId:   req.ResourceId,
		TraceId:      req.TraceId,
		StartTimeMs:  req.StartTime,
		EndTimeMs:    req.EndTime,
		Page:         int64(req.Page),
		PageSize:     int64(req.PageSize),
	}
}

// SearchFilterFromRPC 将 gRPC 查询请求转换为领域查询条件。
func SearchFilterFromRPC(in *pb.SearchAuditLogsRequest) biz.SearchFilter {
	return biz.SearchFilter{
		ServiceName:  in.GetServiceName(),
		ActorId:      in.GetActorId(),
		Action:       in.GetAction(),
		ResourceType: in.GetResourceType(),
		ResourceId:   in.GetResourceId(),
		TraceId:      in.GetTraceId(),
		StartTimeMs:  in.GetStartTime(),
		EndTimeMs:    in.GetEndTime(),
		Page:         in.GetPage(),
		PageSize:     in.GetPageSize(),
	}
}

// ---------------- 输出方向：biz 视图 → 协议响应（仅同名字段拷贝） ----------------

// AuditLogToHTTP 将领域视图转换为 HTTP 响应条目。
func AuditLogToHTTP(v biz.AuditLogView) types.AuditLogItem {
	return types.AuditLogItem{
		Id:           v.Id,
		TraceId:      v.TraceId,
		ServiceName:  v.ServiceName,
		Operation:    v.Operation,
		ActorId:      v.ActorId,
		ActorType:    v.ActorType,
		Action:       v.Action,
		ResourceType: v.ResourceType,
		ResourceId:   v.ResourceId,
		SourceIp:     v.SourceIp,
		UserAgent:    v.UserAgent,
		RequestUri:   v.RequestUri,
		StatusCode:   v.StatusCode,
		RequestBody:  v.RequestBody,
		ResponseBody: v.ResponseBody,
		Metadata:     v.Metadata,
		ErrorMessage: v.ErrorMessage,
		DurationMs:   v.DurationMs,
		CreatedAt:    v.CreatedAtMs,
	}
}

// AuditLogListToHTTP 批量转换领域视图为 HTTP 响应条目。
func AuditLogListToHTTP(rows []biz.AuditLogView) []types.AuditLogItem {
	list := make([]types.AuditLogItem, 0, len(rows))
	for i := range rows {
		list = append(list, AuditLogToHTTP(rows[i]))
	}
	return list
}

// AuditLogToRPC 将领域视图转换为 gRPC 响应条目。
func AuditLogToRPC(v biz.AuditLogView) *pb.AuditLog {
	return &pb.AuditLog{
		Id:           v.Id,
		TraceId:      v.TraceId,
		ServiceName:  v.ServiceName,
		Operation:    v.Operation,
		ActorId:      v.ActorId,
		ActorType:    v.ActorType,
		Action:       v.Action,
		ResourceType: v.ResourceType,
		ResourceId:   v.ResourceId,
		SourceIp:     v.SourceIp,
		UserAgent:    v.UserAgent,
		RequestUri:   v.RequestUri,
		StatusCode:   v.StatusCode,
		RequestBody:  v.RequestBody,
		ResponseBody: v.ResponseBody,
		Metadata:     v.Metadata,
		ErrorMessage: v.ErrorMessage,
		DurationMs:   v.DurationMs,
		CreatedAt:    v.CreatedAtMs,
	}
}

// AuditLogListToRPC 批量转换领域视图为 gRPC 响应条目。
func AuditLogListToRPC(rows []biz.AuditLogView) []*pb.AuditLog {
	list := make([]*pb.AuditLog, 0, len(rows))
	for i := range rows {
		list = append(list, AuditLogToRPC(rows[i]))
	}
	return list
}
