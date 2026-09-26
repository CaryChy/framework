#!/usr/bin/env bash
# 生成 auditlog 服务 mTLS 所需的全部证书（本地开发/测试用，禁止用于生产）：
#   ca.pem / ca-key.pem         自签 CA
#   server.pem / server-key.pem 服务端证书（HTTP 与 gRPC 共用），ServerAuth
#   client.pem / client-key.pem 客户端证书（curl 与 api 访问 rpc 时使用），ClientAuth
#
# 用法：bash deploy/tls/gen.sh
set -euo pipefail

CERT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$CERT_DIR"

DAYS_CA=3650
DAYS_LEAF=825

echo "==> [1/4] 生成自签 CA"
cat > ca.cnf <<'EOF'
[req]
distinguished_name = dn
prompt             = no
x509_extensions    = v3_ca
[dn]
CN = auditlog-ca
[v3_ca]
basicConstraints = critical, CA:TRUE
keyUsage         = critical, keyCertSign, cRLSign
EOF
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout ca-key.pem -out ca.pem -days "$DAYS_CA" \
  -config ca.cnf

echo "==> [2/4] 生成服务端证书（SAN: localhost / auditlog.rpc / 127.0.0.1）"
cat > server-csr.cnf <<'EOF'
[req]
distinguished_name = dn
prompt             = no
req_extensions     = req_ext
[dn]
CN = localhost
[req_ext]
basicConstraints = CA:FALSE
keyUsage         = critical, digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName   = @alt_names
[alt_names]
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

echo "==> [3/4] 生成客户端证书"
cat > client-csr.cnf <<'EOF'
[req]
distinguished_name = dn
prompt             = no
req_extensions     = req_ext
[dn]
CN = auditlog-client
[req_ext]
basicConstraints = CA:FALSE
keyUsage         = critical, digitalSignature, keyEncipherment
extendedKeyUsage = clientAuth
EOF
cat > client-ext.cnf <<'EOF'
basicConstraints = CA:FALSE
keyUsage         = critical, digitalSignature, keyEncipherment
extendedKeyUsage = clientAuth
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
