// 本文件实现 mTLS 客户端证书的 SPIFFE 身份校验（方案 §7、§9、§14.1）：
//   - 身份只取证书 SAN 中的 spiffe://{org}/{project}/{business-system}/{service} URI，忽略 CN；
//   - 证书链验证由 Go TLS 完成，本文件在链验证通过后做身份语义校验；
//   - 组织（信任域）与项目必验；业务系统/服务为可选白名单。
package tls

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/zeromicro/go-zero/core/logx"
)

// spiffeScheme SPIFFE 身份 URI 的 scheme。
const spiffeScheme = "spiffe"

// Identity 从客户端证书 URI SAN 解析出的调用方服务身份。
// 层次：Organization(TrustDomain) → Project → BusinessSystem → Service。
type Identity struct {
	// SPIFFE 证书中的完整 URI，如 spiffe://cari/platform/framework/auditlog-caller。
	SPIFFE string
	// TrustDomain 组织标识，如 cari。
	TrustDomain string
	// Project 项目标识，如 platform / mining-platform。
	Project string
	// BusinessSystem 业务系统标识，如 framework。
	BusinessSystem string
	// Service 具体服务标识。
	Service string
}

// String 返回完整 SPIFFE URI，用于日志与审计。
func (i Identity) String() string {
	return i.SPIFFE
}

// ParseIdentity 从证书的 URI SAN 中解析唯一的 SPIFFE 身份。
// 依据 SPIFFE 规范：一张服务证书有且仅有一个 spiffe:// URI；
// 缺失、多于一个、格式非法均拒绝（宁可握手失败也不接受模糊身份）。
func ParseIdentity(cert *x509.Certificate) (*Identity, error) {
	var spiffeURIs []string
	for _, u := range cert.URIs {
		if u != nil && strings.EqualFold(u.Scheme, spiffeScheme) {
			spiffeURIs = append(spiffeURIs, u.String())
		}
	}
	if len(spiffeURIs) == 0 {
		return nil, errors.New("客户端证书缺少 spiffe:// URI SAN，无法确认调用方服务身份")
	}
	if len(spiffeURIs) > 1 {
		return nil, fmt.Errorf("客户端证书包含 %d 个 spiffe URI SAN，身份不唯一: %v", len(spiffeURIs), spiffeURIs)
	}

	raw := spiffeURIs[0]
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("解析 spiffe URI 失败 %q: %w", raw, err)
	}

	// SPIFFE 约束：不允许 userinfo/port/query/fragment；trust domain 不可为空。
	if u.User != nil {
		return nil, fmt.Errorf("spiffe URI 不允许包含 userinfo: %q", raw)
	}
	if u.Port() != "" {
		return nil, fmt.Errorf("spiffe URI 不允许包含端口: %q", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("spiffe URI 不允许包含 query/fragment: %q", raw)
	}
	trustDomain := strings.ToLower(u.Hostname())
	if trustDomain == "" {
		return nil, fmt.Errorf("spiffe URI 缺少组织（信任域）: %q", raw)
	}

	// 路径形如 /{project}/{business-system}/{service}，三段必填；
	// 高安全场景允许追加 /instance/{id} 后缀，解析时忽略。
	path := strings.Trim(u.Path, "/")
	segments := strings.Split(path, "/")
	if len(segments) < 3 {
		return nil, fmt.Errorf("spiffe URI 路径必须为 /{project}/{business-system}/{service}: %q", raw)
	}
	for _, seg := range segments[:3] {
		if seg == "" {
			return nil, fmt.Errorf("spiffe URI 身份层次不允许为空段: %q", raw)
		}
	}

	return &Identity{
		SPIFFE:         raw,
		TrustDomain:    trustDomain,
		Project:        segments[0],
		BusinessSystem: segments[1],
		Service:        segments[2],
	}, nil
}

// Policy 服务端对调用方证书的身份校验策略，gRPC 与 HTTP 共用同一份实现。
// 组织/项目不从配置读取：启动时从本服务证书的 SPIFFE URI 派生，调用方必须同组织同项目。
type Policy struct {
	// AllowedBusinessSystems 允许的业务系统白名单；空表示同组织/项目内不限制业务系统。
	AllowedBusinessSystems []string
	// AllowedServices 允许的服务白名单；空表示不限制具体服务。
	AllowedServices []string
}

// identityVerifier 持有从【本服务证书】派生的组织/项目基准与可选白名单，
// 每个连接的客户端证书都与该基准比较。
type identityVerifier struct {
	self   Identity // 本服务身份，组织/项目基准来源
	policy Policy
}

// newVerifier 从本服务加载的叶子证书解析出 SPIFFE 身份，构造调用方校验器。
// 启用 mTLS 时服务端证书必须携带合法 SPIFFE URI，否则无法判定组织/项目边界，启动即失败。
func (p Policy) newVerifier(serverLeaf *x509.Certificate) (*identityVerifier, error) {
	self, err := ParseIdentity(serverLeaf)
	if err != nil {
		return nil, fmt.Errorf("无法从本服务证书 %q 派生组织/项目身份基准: %w",
			serverLeaf.Subject.String(), err)
	}
	return &identityVerifier{self: *self, policy: p}, nil
}

// verify 校验已通过链验证的调用方叶子证书：必须与本服务同组织、同项目，
// 再按可选白名单限制业务系统/服务。返回解析出的调用方身份。
func (v *identityVerifier) verify(leaf *x509.Certificate) (*Identity, error) {
	id, err := ParseIdentity(leaf)
	if err != nil {
		return nil, err
	}

	// 组织（信任域）必须一致：不同组织即使证书链有效也拒绝。
	if id.TrustDomain != v.self.TrustDomain {
		return nil, fmt.Errorf("调用方组织与本服务不一致: 调用方=%q，本服务=%q（身份=%s）",
			id.TrustDomain, v.self.TrustDomain, id.SPIFFE)
	}
	// Project 必验：只接受同一项目下的服务调用。
	if id.Project != v.self.Project {
		return nil, fmt.Errorf("调用方项目与本服务不一致: 调用方=%q，本服务=%q（身份=%s）",
			id.Project, v.self.Project, id.SPIFFE)
	}
	// Business System 可选：配置白名单后才校验。
	if len(v.policy.AllowedBusinessSystems) > 0 && !contains(v.policy.AllowedBusinessSystems, id.BusinessSystem) {
		return nil, fmt.Errorf("调用方业务系统不在允许列表: 证书=%q，允许=%v（身份=%s）",
			id.BusinessSystem, v.policy.AllowedBusinessSystems, id.SPIFFE)
	}
	// Service 可选：配置白名单后才校验。
	if len(v.policy.AllowedServices) > 0 && !contains(v.policy.AllowedServices, id.Service) {
		return nil, fmt.Errorf("调用方服务不在允许列表: 证书=%q，允许=%v（身份=%s）",
			id.Service, v.policy.AllowedServices, id.SPIFFE)
	}
	return id, nil
}

// verifyPeerCertificate 是挂到 tls.Config.VerifyPeerCertificate 的回调。
// 该回调在 Go TLS 完成证书链/EKU 验证（Root 必验）之后执行，
// 失败将导致 mTLS 握手被拒（TLS bad_certificate alert）。
func (v *identityVerifier) verifyPeerCertificate(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
	leaf, err := verifiedLeaf(rawCerts, verifiedChains)
	if err != nil {
		logx.Errorw("mTLS 客户端证书不可读", logx.Field("error", err))
		return err
	}

	id, err := v.verify(leaf)
	if err != nil {
		// 安全事件需可观测：记录失败原因与调用方证书主题，便于审计排查。
		logx.Errorw("mTLS 调用方身份校验失败",
			logx.Field("subject", leaf.Subject.String()),
			logx.Field("issuer", leaf.Issuer.String()),
			logx.Field("server_spiffe_id", v.self.SPIFFE),
			logx.Field("error", err.Error()),
		)
		return err
	}

	logx.Infow("mTLS 调用方身份校验通过",
		logx.Field("spiffe_id", id.SPIFFE),
		logx.Field("organization", id.TrustDomain),
		logx.Field("project", id.Project),
		logx.Field("business_system", id.BusinessSystem),
		logx.Field("service", id.Service),
	)
	return nil
}

// verifiedLeaf 优先取标准链验证结果中的叶子证书；极端情况下回调未带链时
// （如自定义 ClientAuth）回退为自行解析对端提交的第一张证书。
func verifiedLeaf(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) (*x509.Certificate, error) {
	if len(verifiedChains) > 0 && len(verifiedChains[0]) > 0 {
		return verifiedChains[0][0], nil
	}
	if len(rawCerts) == 0 {
		return nil, errors.New("对端未提供客户端证书")
	}
	cert, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return nil, fmt.Errorf("解析客户端叶子证书失败: %w", err)
	}
	return cert, nil
}

func contains(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}
