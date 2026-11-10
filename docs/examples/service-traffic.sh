#!/usr/bin/env bash
separator=""
for service in easytier.service frps.service sing-box.service; do
  printf '%s' "$separator"
  systemctl show "$service" -p IPIngressBytes -p IPEgressBytes |
  awk -F= -v name="$service" '
    function mb(value) {
      return value ~ /^[0-9]+$/ && value != "18446744073709551615" \
        ? sprintf("%.2f MB", value / 1000000) : "暂无统计"
    }
    /^IPIngressBytes=/ { rx = $2 }
    /^IPEgressBytes=/ { tx = $2 }
    END {
      print name
      print "接收：" mb(rx)
      print "发送：" mb(tx)
    }
  '
  separator=$'\n'
done
