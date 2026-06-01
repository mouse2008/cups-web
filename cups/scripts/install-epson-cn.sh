#!/usr/bin/env bash
# Epson 国行私有驱动：仅 amd64 best-effort 安装。
#
# `epson-inkjet-printer-201601w` 与 `epson-printer-utility` 是 Epson 中国区
# 发布的**闭源专有** .deb 包（无源码，无 arm64/armhf 二进制），覆盖 L380/L455
# 等国行早期喷墨机型。对应功能大部分可以被 Debian 自带的 `printer-driver-escpr`
# 覆盖，但原厂 PPD 在墨水检测、尺寸预设等细节上更完整。
#
# ⚠️ 原下载源 download-center.epson.com.cn 的 UUID 会定期轮换导致 URL 失效，
# 因此把 .deb 镜像到本仓库的 GitHub Releases（cups-driver tag）。
# 但镜像附件可能被删除/私有化，且该驱动只影响少量国行老机型；为了不让
# 非 Epson 用户（例如仅使用 LDAP 功能的部署）被可选专有驱动卡死，这里采用
# **best-effort**：下载 / dpkg 任一步失败都打印 WARNING 并 exit 0 跳过安装。
# arm64/armhf 在脚本入口直接退出，不受影响。
# 升级方法：把新版 .deb 上传到 https://github.com/hanxi/cups-web/releases 的
# cups-driver tag，更新下方 DEB 变量即可。

set -eo pipefail

warn_skip() {
    echo "[epson-cn] WARNING: $*"
    echo "[epson-cn] WARNING: proprietary Epson CN driver skipped; build continues"
    exit 0
}


# 仅 amd64 安装；其他架构静默退出（exit 0，不影响整个 build）
ARCH="$(dpkg --print-architecture)"
if [ "${ARCH}" != "amd64" ]; then
    echo "[epson-cn] skip: arch=${ARCH} (only amd64 supported)"
    exit 0
fi

# ────────────────────────────────────────────────────────────────────
# 配置
# ────────────────────────────────────────────────────────────────────
EPSON_PROP_DRIVER_DEB="epson-inkjet-printer-201601w_1.0.1-1_amd64.deb"
EPSON_PROP_UTILITY_DEB="epson-printer-utility_1.2.2-1_amd64.deb"
EPSON_PROP_UA="Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

EPSON_DRV_URL="https://github.com/hanxi/cups-web/releases/download/cups-driver/${EPSON_PROP_DRIVER_DEB}"
EPSON_UTIL_URL="https://github.com/hanxi/cups-web/releases/download/cups-driver/${EPSON_PROP_UTILITY_DEB}"

# ────────────────────────────────────────────────────────────────────
# 下载 & dpkg
# ────────────────────────────────────────────────────────────────────
BUILD_DIR="$(mktemp -d /tmp/epson-cn.XXXXXX)"
trap 'rm -rf "${BUILD_DIR}"' EXIT

cd "${BUILD_DIR}"

echo "[epson-cn] downloading ${EPSON_DRV_URL}"
if ! wget --tries=3 --timeout=60 --retry-connrefused \
     --user-agent="${EPSON_PROP_UA}" \
     -O "${EPSON_PROP_DRIVER_DEB}" "${EPSON_DRV_URL}"; then
    rm -f "${EPSON_PROP_DRIVER_DEB}"
    warn_skip "failed to download ${EPSON_PROP_DRIVER_DEB}"
fi

echo "[epson-cn] downloading ${EPSON_UTIL_URL}"
if ! wget --tries=3 --timeout=60 --retry-connrefused \
     --user-agent="${EPSON_PROP_UA}" \
     -O "${EPSON_PROP_UTILITY_DEB}" "${EPSON_UTIL_URL}"; then
    rm -f "${EPSON_PROP_UTILITY_DEB}"
    warn_skip "failed to download ${EPSON_PROP_UTILITY_DEB}"
fi

# dpkg -i 失败时用 apt-get -f install 兜底处理依赖；仍失败则跳过，不阻塞整体镜像构建。
if ! dpkg -i ./*.deb; then
    if ! apt-get install -y -f --no-install-recommends; then
        warn_skip "failed to install Epson CN proprietary packages"
    fi
fi

echo "[epson-cn] installed Epson CN proprietary driver + utility"
rm -rf /var/lib/apt/lists/*
