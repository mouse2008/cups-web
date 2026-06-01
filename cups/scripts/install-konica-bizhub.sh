#!/usr/bin/env bash
# 柯尼卡美能达 bizhub 3000MF 黑白激光打印机驱动：amd64 + arm64 best-effort 安装。
#
# 背景（issue #35）：
# 柯尼卡美能达官方未把任何 Linux 驱动上传 Debian 仓库；其官方下载站
# (konicaminolta.com.cn) 只针对国产化平台（银河麒麟 / UOS）发布了 .deb，
# 原始格式是 .7z（中文顶层目录「银河麒麟」），且真实下载点是 IIS 302 跳
# CloudFront 签名 URL，fileId 长期稳定但下载链路不易在 CI 里稳定复现。
# 所以这里只从我们自维护的 GitHub Releases 镜像下载 tar.gz——已经把官方
# .7z 解包后重新打成 .tar.gz 上传，省掉了 p7zip-full 依赖与中文路径处理。
#
# ────────────────────────────────────────────────────────────────────
# 架构覆盖说明
# ────────────────────────────────────────────────────────────────────
# tar.gz 解压后顶层目录与 tarball 同名（bizhub3000mfpdrvchn_<ver>/），按
# 架构分四个子目录：
#   amd64/        bizhub3000mfpdrvchn_<ver>_amd64.deb       → amd64
#   arm64/        bizhub3000mfpdrvchn_<ver>_arm64.deb       → arm64
#   loongarch64/  bizhub3000mfpdrvchn_<ver>_loongarch64.deb → 龙芯（本镜像未发布）
#   mips64/       bizhub3000mfpdrvchn_<ver>_mips64el.deb    → MIPS（本镜像未发布）
# 每个架构同时包含 konicaminoltascan1（扫描驱动）的同名 .deb，
# **本脚本不安装扫描驱动**——理由：
#   ① 本仓库是 Web 打印工具，scan 没业务诉求；
#   ② konicaminoltascan1 依赖 sane-utils/libsane 等扫描栈，trixie 上的
#      包名/ABI 跟银河麒麟可能错位，装失败会让 dpkg 回退跑 `apt -f install`
#      拖慢构建甚至误装一堆无用依赖。
# armhf/armel 没有 32-bit ARM 包，脚本入口直接 skip。
#
# ────────────────────────────────────────────────────────────────────
# 下载策略
# ────────────────────────────────────────────────────────────────────
# 与 install-escpr2.sh 同模式：只从仓库自维护的 GitHub Releases 镜像
# （tag 统一为 cups-driver）下载，避免厂商 IIS / CloudFront 签名 URL
# 在 CI 里的不稳定性（fileId 失效、URL 签名过期、TLS 指纹风控等）。
# 但该专有驱动只是可选增强，不应阻塞与 Konica 无关的部署（例如只启用 LDAP
# 认证的实例）。因此这里采用 best-effort：下载 / 解压 / 安装失败时打印
# WARNING 并 exit 0 跳过安装。
# 升级版本：①在本仓库 cups-driver release 上传新版 tar.gz；②修改下方
# KM_VERSION / KM_TARBALL / KM_MIRROR_URL 三个变量。

set -eo pipefail

warn_skip() {
    echo "[konica-bizhub] WARNING: $*"
    echo "[konica-bizhub] WARNING: Konica Minolta proprietary driver skipped; build continues"
    exit 0
}


# ────────────────────────────────────────────────────────────────────
# 架构判断 → 选择 tarball 内的子目录与 .deb 名
# ────────────────────────────────────────────────────────────────────
ARCH="$(dpkg --print-architecture)"
case "${ARCH}" in
    amd64)
        KM_DEB_ARCH="amd64"
        ;;
    arm64)
        KM_DEB_ARCH="arm64"
        ;;
    *)
        echo "[konica-bizhub] skip: arch=${ARCH} (no ${ARCH} binary; supported: amd64/arm64)"
        exit 0
        ;;
esac

# ────────────────────────────────────────────────────────────────────
# 配置（升级版本时同步更新这一组）
# ────────────────────────────────────────────────────────────────────
KM_VERSION="1.0.0-1"
KM_TARBALL="bizhub3000mfpdrvchn_${KM_VERSION}.tar.gz"
KM_MIRROR_URL="https://github.com/hanxi/cups-web/releases/download/cups-driver/${KM_TARBALL}"

# tarball 内 .deb 路径形如 bizhub3000mfpdrvchn_<ver>/<arch>/<deb>，
# 用 find -name 按 .deb 文件名兜底定位，避免硬编码顶层目录路径。
KM_DEB_NAME="bizhub3000mfpdrvchn_${KM_VERSION}_${KM_DEB_ARCH}.deb"

# ────────────────────────────────────────────────────────────────────
# 下载 & 解压 & dpkg
# ────────────────────────────────────────────────────────────────────
BUILD_DIR="$(mktemp -d /tmp/konica-bizhub.XXXXXX)"
trap 'rm -rf "${BUILD_DIR}"' EXIT

cd "${BUILD_DIR}"

echo "[konica-bizhub] arch=${ARCH} → ${KM_DEB_NAME}"
echo "[konica-bizhub] downloading from mirror ${KM_MIRROR_URL}"
curl -fL --retry 3 --retry-delay 3 -o "${KM_TARBALL}" "${KM_MIRROR_URL}" || warn_skip "failed to download Konica tarball"

mkdir -p extracted
tar xzf "${KM_TARBALL}" -C extracted || warn_skip "failed to extract Konica tarball"

# find 兜底定位 .deb：tarball 顶层目录跟随版本号变化，find 比硬编码更稳。
DEB_PATH="$(find extracted -type f -name "${KM_DEB_NAME}" -print -quit 2>/dev/null || true)"

if [ -z "${DEB_PATH}" ]; then
    echo "[konica-bizhub] WARNING: deb file not found in tarball"
    echo "[konica-bizhub]   expected: ${KM_DEB_NAME}"
    echo "[konica-bizhub]   tarball layout:"
    find extracted -maxdepth 4 -type f -name "*.deb" || true
    warn_skip "expected Konica deb missing from tarball"
fi

echo "[konica-bizhub] installing ${DEB_PATH}"

# dpkg -i 失败时用 apt-get -f install 兜底处理依赖（与 install-epson-cn.sh 同模式）。
dpkg -i "${DEB_PATH}" || apt-get install -y -f --no-install-recommends || warn_skip "failed to install Konica deb"

echo "[konica-bizhub] installed Konica Minolta bizhub 3000MF driver v${KM_VERSION} (${KM_DEB_ARCH})"
rm -rf /var/lib/apt/lists/*
