#!/usr/bin/env bash
# 生成 auditlog 服务 mTLS 所需的全部证书（本地开发/测试用，禁止用于生产）：
#   ca.pem / ca-key.pem         自签 CA（O=cari）
#   server.pem / server-key.pem 服务端证书（HTTP 与 gRPC 共用），ServerAuth
#   client.pem / client-key.pem 客户端证书（curl 与上游服务调用时使用），ClientAuth
#
# 身份约定（对应《服务身份认证与服务间安全通信方案》§6/§7/§9）：
#   - 身份主体不取 CN，统一写入 SAN 中的 SPIFFE URI：
#       server: spiffe://cari/platform/framework/auditlog
#       client: spiffe://cari/platform/framework/auditlog-client
#   - 服务端 mTLS 除验证证书链（Root）外，强制要求调用方同组织(cari)、同项目(platform)，
#     因此 client 证书的 project 段必须为 platform，否则握手被拒。
#
# 用法：bash deploy/tls/gen.sh
set -euo pipefail

CERT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$CERT_DIR"

DAYS_CA=3650
DAYS_LEAF=825

echo "==> [1/4] 生成自签 CA（Organization=cari）"
cat > ca.cnf <<'EOF'
[req]
distinguished_name = dn
prompt             = no
x509_extensions    = v3_ca
[dn]
O  = cari
OU = platform
CN = auditlog-ca
[v3_ca]
basicConstraints = critical, CA:TRUE
keyUsage         = critical, keyCertSign, cRLSign
EOF
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout ca-key.pem -out ca.pem -days "$DAYS_CA" \
  -config ca.cnf

echo "==> [2/4] 生成服务端证书（SPIFFE: spiffe://cari/platform/framework/auditlog）"
cat > server-csr.cnf <<'EOF'
[req]
distinguished_name = dn
prompt             = no
req_extensions     = req_ext
[dn]
O  = cari
OU = platform/framework
CN = auditlog-server
[req_ext]
basicConstraints = CA:FALSE
keyUsage         = critical, digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName   = @alt_names
[alt_names]
URI.1 = spiffe://cari/platform/framework/auditlog
DNS.1 = localhost
DNS.2 = auditlog.rpc
IP.1  = 127.0.0.1
EOF
cat > server-ext.cnf <<'EOF'
basicConstraints = CA:FALSE
keyUsage         = critical, digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName   = @alt_names
[alt_names]
URI.1 = spiffe://cari/platform/framework/auditlog
DNS.1 = localhost
DNS.2 = auditlog.rpc
IP.1  = 127.0.0.1
EOF
openssl req -new -newkey rsa:2048 -nodes \
  -keyout server-key.pem -out server.csr -config server-csr.cnf
openssl x509 -req -in server.csr \
  -CA ca.pem -CAkey ca-key.pem -CAcreateserial \
  -out server.pem -days "$DAYS_LEAF" \
  -extfile server-ext.cnf

echo "==> [3/4] 生成客户端证书（SPIFFE: spiffe://cari/platform/framework/auditlog-client）"
cat > client-csr.cnf <<'EOF'
[req]
distinguished_name = dn
prompt             = no
req_extensions     = req_ext
[dn]
O  = cari
OU = platform/framework
CN = auditlog-client
[req_ext]
basicConstraints = CA:FALSE
keyUsage         = critical, digitalSignature, keyEncipherment
extendedKeyUsage = clientAuth
subjectAltName   = @alt_names
[alt_names]
URI.1 = spiffe://cari/platform/framework/auditlog-client
EOF
cat > client-ext.cnf <<'EOF'
basicConstraints = CA:FALSE
keyUsage         = critical, digitalSignature, keyEncipherment
extendedKeyUsage = clientAuth
subjectAltName   = @alt_names
[alt_names]
URI.1 = spiffe://cari/platform/framework/auditlog-client
EOF
openssl req -new -newkey rsa:2048 -nodes \
  -keyout client-key.pem -out client.csr -config client-csr.cnf
openssl x509 -req -in client.csr \
  -CA ca.pem -CAkey ca-key.pem -CAcreateserial \
  -out client.pem -days "$DAYS_LEAF" \
  -extfile client-ext.cnf

echo "==> [4/4] 清理中间文件并收敛私钥权限"
rm -f *.csr *.srl *.cnf
chmod 600 *-key.pem
chmod 644 *.pem

echo "完成，证书输出目录：$CERT_DIR"
ls -1 "$CERT_DIR"/*.pem
