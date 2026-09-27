#!/usr/bin/env bash
# auditlog mTLS 证书轮换脚本。
#
# 背景：旧证书曾误入 git 历史，必须通过轮换使其彻底失效（仅从索引移除不够）。
#
# 两种轮换模式：
#   bash rotate.sh leaf    【推荐，日常轮换】只换 server/client 叶子证书，CA 不变。
#                          新旧证书由同一 CA 签发，mTLS 互信不中断，滚动重启服务即可。
#   bash rotate.sh ca      【CA 泄露或根证书到期时用】重新生成 CA 及全部叶子证书。
#                          注意：CA 更换后，所有调用方（curl/上游网关/其他服务）必须同步
#                          更新信任的新 ca.pem，否则 mTLS 握手立即失败。需要灰度过渡。
#   bash rotate.sh check   只检查现有证书有效期与即将到期提醒，不做任何变更。
#
# 安全设计：
#   - 轮换前自动备份到 backup/<时间戳>/，校验失败自动回滚；
#   - 新证书先写入临时目录，openssl verify 通过后才原子替换，避免半截状态；
#   - 私钥权限收敛 600，轮换后提示重启服务与同步分发客户端证书。
set -euo pipefail

CERT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$CERT_DIR"

DAYS_CA=3650        # CA 有效期 10 年
DAYS_LEAF=825       # 叶子证书有效期（Apple/主流客户端上限）
WARN_DAYS=30        # 剩余天数低于该值时 check 模式给出告警

# ---------- 通用工具 ----------

# 输出证书主题与到期时间。
cert_info() {
  local cert="$1"
  openssl x509 -in "$cert" -noout -subject -enddate 2>/dev/null
}

# 证书剩余有效天数；证书不存在或解析失败返回 -1。
days_left() {
  local cert="$1"
  [ -f "$cert" ] || { echo -1; return; }
  local end_ts now_ts
  end_ts=$(openssl x509 -in "$cert" -noout -enddate | cut -d= -f2 | xargs -I{} date -j -f "%b %d %T %Y %Z" "{}" +%s 2>/dev/null || date -d "$(openssl x509 -in "$cert" -noout -enddate | cut -d= -f2)" +%s)
  now_ts=$(date +%s)
  echo $(( (end_ts - now_ts) / 86400 ))
}

# 备份当前证书到 backup/<时间戳>/，返回备份目录。
backup_current() {
  local ts dir
  ts=$(date +%Y%m%d-%H%M%S)
  dir="backup/$ts"
  mkdir -p "$dir"
  # 仅备份存在的 pem，避免首次运行时报错。
  for f in ca.pem ca-key.pem server.pem server-key.pem client.pem client-key.pem; do
    [ -f "$f" ] && cp -p "$f" "$dir/"
  done
  echo "$dir"
}

# ---------- 生成逻辑（与 gen.sh 保持一致） ----------

gen_ca() {
  local out_dir="$1"
  cat > "$out_dir/ca.cnf" <<'EOF'
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
    -keyout "$out_dir/ca-key.pem" -out "$out_dir/ca.pem" -days "$DAYS_CA" \
    -config "$out_dir/ca.cnf" 2>/dev/null
}

gen_server() {
  local out_dir="$1" ca_dir="$2"
  cat > "$out_dir/server-csr.cnf" <<'EOF'
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
  cat > "$out_dir/server-ext.cnf" <<'EOF'
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
    -keyout "$out_dir/server-key.pem" -out "$out_dir/server.csr" -config "$out_dir/server-csr.cnf" 2>/dev/null
  openssl x509 -req -in "$out_dir/server.csr" \
    -CA "$ca_dir/ca.pem" -CAkey "$ca_dir/ca-key.pem" -CAcreateserial \
    -out "$out_dir/server.pem" -days "$DAYS_LEAF" \
    -extfile "$out_dir/server-ext.cnf" 2>/dev/null
}

gen_client() {
  local out_dir="$1" ca_dir="$2"
  cat > "$out_dir/client-csr.cnf" <<'EOF'
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
  cat > "$out_dir/client-ext.cnf" <<'EOF'
basicConstraints = CA:FALSE
keyUsage         = critical, digitalSignature, keyEncipherment
extendedKeyUsage = clientAuth
subjectAltName   = @alt_names
[alt_names]
URI.1 = spiffe://cari/platform/framework/auditlog-client
EOF
  openssl req -new -newkey rsa:2048 -nodes \
    -keyout "$out_dir/client-key.pem" -out "$out_dir/client.csr" -config "$out_dir/client-csr.cnf" 2>/dev/null
  openssl x509 -req -in "$out_dir/client.csr" \
    -CA "$ca_dir/ca.pem" -CAkey "$ca_dir/ca-key.pem" -CAcreateserial \
    -out "$out_dir/client.pem" -days "$DAYS_LEAF" \
    -extfile "$out_dir/client-ext.cnf" 2>/dev/null
}

# 校验临时目录中的证书链是否有效，并断言叶子证书携带 SPIFFE URI SAN。
# 用 -text 输出而非 -ext：兼容 macOS 自带 LibreSSL（不支持 x509 -ext）。
assert_spiffe_san() {
  local cert="$1" want="$2"
  openssl x509 -in "$cert" -noout -text \
    | tr -d '\n' | grep -q "Subject Alternative Name.*URI:${want}" \
    || { echo "错误：${cert} 缺少预期的 SPIFFE URI SAN: ${want}"; return 1; }
}

verify_bundle() {
  local dir="$1"
  openssl verify -CAfile "$dir/ca.pem" "$dir/server.pem" >/dev/null
  openssl verify -CAfile "$dir/ca.pem" "$dir/client.pem" >/dev/null
  assert_spiffe_san "$dir/server.pem" 'spiffe://cari/platform/framework/auditlog'
  assert_spiffe_san "$dir/client.pem" 'spiffe://cari/platform/framework/auditlog-client'
}

# ---------- 模式：check ----------

do_check() {
  echo "==> 证书有效期检查（告警阈值：剩余 < ${WARN_DAYS} 天）"
  local rc=0
  for pair in "ca.pem:CA" "server.pem:server" "client.pem:client"; do
    local f="${pair%%:*}" label="${pair##*:}"
    if [ ! -f "$f" ]; then
      echo "  [缺失] $f"
      rc=1
      continue
    fi
    local left
    left=$(days_left "$f")
    local mark="OK"
    if [ "$left" -lt 0 ]; then mark="已过期"; rc=1;
    elif [ "$left" -lt "$WARN_DAYS" ]; then mark="即将到期，请尽快轮换"; rc=1; fi
    echo "  [${label}] ${f} 剩余 ${left} 天（${mark}）"
  done
  return $rc
}

# ---------- 模式：leaf（仅换叶子证书，CA 不变） ----------

do_leaf() {
  [ -f ca.pem ] && [ -f ca-key.pem ] || { echo "错误：当前目录缺少 ca.pem/ca-key.pem，无法只换叶子证书；如需重建 CA 请用 'rotate.sh ca'"; exit 1; }

  echo "==> [1/4] 备份当前证书"
  local backup_dir
  backup_dir=$(backup_current)
  echo "    备份到 $backup_dir"

  echo "==> [2/4] 在临时目录生成新 server/client 证书（沿用现有 CA）"
  local tmp
  tmp=$(mktemp -d)
  cp -p ca.pem ca-key.pem "$tmp/"
  gen_server "$tmp" "$tmp"
  gen_client "$tmp" "$tmp"

  echo "==> [3/4] 校验新证书链"
  if ! verify_bundle "$tmp"; then
    echo "错误：新证书校验失败，已放弃轮换，原证书未改动"
    rm -rf "$tmp"
    exit 1
  fi

  echo "==> [4/4] 原子替换并收敛权限"
  mv "$tmp/server.pem" "$tmp/server-key.pem" "$tmp/client.pem" "$tmp/client-key.pem" "$CERT_DIR/"
  rm -rf "$tmp"
  chmod 600 server-key.pem client-key.pem
  chmod 644 server.pem client.pem

  echo
  echo "叶子证书轮换完成："
  cert_info server.pem
  cert_info client.pem
  echo
  echo "后续步骤："
  echo "  1. 滚动重启 auditlog 服务加载新证书（当前实现不支持热加载）"
  echo "  2. 将新的 client.pem / client-key.pem 分发给所有调用方（curl / 上游网关）"
  echo "  3. 确认全部调用方切换后，删除备份目录中的旧私钥：rm -rf $backup_dir"
  echo "  4. CA 未变更，调用方信任的 ca.pem 无需更新"
}

# ---------- 模式：ca（重建 CA + 全部叶子证书） ----------

do_ca() {
  echo "警告：CA 轮换后，所有调用方必须同步更新信任的 ca.pem，否则 mTLS 握手立即失败。"
  echo "      请在低峰期执行，并确保新 ca.pem 的分发通道已就绪。"
  read -r -p "确认继续？[y/N] " ans
  case "$ans" in
    y|Y|yes|YES) ;;
    *) echo "已取消"; exit 0;;
  esac

  echo "==> [1/4] 备份当前证书"
  local backup_dir
  backup_dir=$(backup_current)
  echo "    备份到 $backup_dir"

  echo "==> [2/4] 在临时目录生成新 CA 与 server/client 证书"
  local tmp
  tmp=$(mktemp -d)
  gen_ca "$tmp"
  gen_server "$tmp" "$tmp"
  gen_client "$tmp" "$tmp"

  echo "==> [3/4] 校验新证书链"
  if ! verify_bundle "$tmp"; then
    echo "错误：新证书校验失败，已放弃轮换，原证书未改动"
    rm -rf "$tmp"
    exit 1
  fi

  echo "==> [4/4] 原子替换并收敛权限"
  mv "$tmp"/*.pem "$CERT_DIR/"
  rm -rf "$tmp"
  chmod 600 ca-key.pem server-key.pem client-key.pem
  chmod 644 ca.pem server.pem client.pem

  echo
  echo "CA 轮换完成："
  cert_info ca.pem
  cert_info server.pem
  cert_info client.pem
  echo
  echo "后续步骤（顺序敏感）："
  echo "  1. 先将新的 ca.pem 分发给所有调用方并使其生效（此时服务端仍是旧证书，新旧 CA 不同时受信会断连）"
  echo "     —— 若无法做到平滑过渡，可采用短暂停服窗口整体切换"
  echo "  2. 滚动重启 auditlog 服务加载新证书"
  echo "  3. 分发新的 client.pem / client-key.pem 给调用方"
  echo "  4. 验证全链路 mTLS 通过后，删除备份目录中的旧私钥：rm -rf $backup_dir"
}

# ---------- 入口 ----------

case "${1:-}" in
  leaf)  do_leaf ;;
  ca)    do_ca ;;
  check) do_check ;;
  *)
    echo "用法：bash $0 {leaf|ca|check}"
    echo "  leaf   只换 server/client 叶子证书（CA 不变，日常轮换推荐）"
    echo "  ca     重建 CA 及全部证书（CA 泄露/到期时用，需灰度过渡）"
    echo "  check  检查现有证书有效期，不做变更"
    exit 2
    ;;
esac
