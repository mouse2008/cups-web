#!/usr/bin/env bash
# 编译并安装 Epson ESC/P-R 2 驱动。
#
# 支持 ET-18100, L8050, L8160, WF-7840 等新款 Epson 喷墨打印机的完整功能（含无边距打印）。
# Debian 官方仓库不提供此包，从 Epson 源码编译。
#
# 注：apt 阶段的 libcups2-dev / libcupsimage2-dev 不再单独安装——上一步源码编译
# CUPS 的 `make install` 已把 cups/cupsimage 的头文件与 .so 放入 /usr/include 与
# /usr/lib，ESC/P-R 2 的 configure 会通过 cups-config 找到新编译出的 libcups，
# 避免 apt 版 -dev 包回踩覆盖源码装好的 /usr/include/cups/*.h。
#
# ⚠️ 下载策略：
# Epson 官方下载中心（download-center.epson.com）挂在 Akamai CDN 后面，对请求做
# UA/Header/TLS 指纹等多维度风控，HTTP 403 概率高且 UUID 会被 Epson 定期轮换，
# 不适合作为稳定的 CI 构建源。所以这里只从我们自维护的 GitHub Releases 镜像下载
# 源码 tarball / 预编译 deb，下载失败则脚本以非零退出码结束（fail-fast），
# 避免发布镜像里缺少 ESCPR2 驱动却静默成功。
#
# 安装策略：
#   amd64 / armhf → 直接 dpkg 安装预编译 .deb（省 5~10 分钟编译时间）
#   其他架构（arm64 等） → 回退到源码编译
# 升级版本：① 在本仓库的 Releases 里上传新版 tarball + amd64/armhf deb；
#          ② 修改下方 ESCPR2_VERSION / ESCPR2_MIRROR_URL /
#             ESCPR2_DEB_AMD64_URL / ESCPR2_DEB_ARMHF_URL。

set -eo pipefail

warn_skip() {
    echo "[escpr2] WARNING: $1"
    echo "[escpr2] WARNING: continuing without Epson ESC/P-R 2 proprietary driver; base printer-driver-escpr remains available"
    exit 0
}

download_or_skip() {
    local url="$1"
    local output="$2"
    if ! curl -fL --retry 5 --retry-all-errors --connect-timeout 20 --max-time 300 -o "${output}" "${url}"; then
        warn_skip "download failed from ${url}"
    fi
}

# ────────────────────────────────────────────────────────────────────
# 配置
# ────────────────────────────────────────────────────────────────────
ESCPR2_VERSION="1.2.39"
# 镜像 release tag 统一为 cups-driver，多个第三方驱动 tarball / deb 共用一个 release，
# 升级版本时上传新文件到同一 release，无需创建新 tag。
ESCPR2_MIRROR_URL="https://github.com/hanxi/cups-web/releases/download/cups-driver/epson-inkjet-printer-escpr2-1.2.39-1.tar.gz"
ESCPR2_DEB_AMD64_URL="https://github.com/hanxi/cups-web/releases/download/cups-driver/epson-inkjet-printer-escpr2_1.2.39-1_amd64.deb"
ESCPR2_DEB_ARMHF_URL="https://github.com/hanxi/cups-web/releases/download/cups-driver/epson-inkjet-printer-escpr2_1.2.39_armhf.deb"

BUILD_DIR="$(mktemp -d /tmp/escpr2-build.XXXXXX)"
trap 'rm -rf "${BUILD_DIR}"' EXIT

cd "${BUILD_DIR}"

# ────────────────────────────────────────────────────────────────────
# 架构判断 → amd64/armhf 直接 dpkg 安装预编译 .deb，其他架构源码编译
# ────────────────────────────────────────────────────────────────────
# amd64/armhf 是发布镜像最常见的两类目标，直接装 .deb 可以省掉容器里
# autoreconf/gcc 的 5~10 分钟编译时间。arm64（树莓派 4/5、Apple Silicon
# 等）暂时没有预编译包，回退到源码编译。
ARCH="$(dpkg --print-architecture)"
case "${ARCH}" in
    amd64)
        ESCPR2_DEB_URL="${ESCPR2_DEB_AMD64_URL}"
        ;;
    armhf)
        ESCPR2_DEB_URL="${ESCPR2_DEB_ARMHF_URL}"
        ;;
    *)
        ESCPR2_DEB_URL=""
        ;;
esac

if [ -n "${ESCPR2_DEB_URL}" ]; then
    ESCPR2_DEB_FILE="$(basename "${ESCPR2_DEB_URL}")"
    echo "[escpr2] arch=${ARCH} → installing prebuilt deb ${ESCPR2_DEB_FILE}"
    echo "[escpr2] downloading from mirror ${ESCPR2_DEB_URL}"
    download_or_skip "${ESCPR2_DEB_URL}" "${ESCPR2_DEB_FILE}"

    dpkg -i "${ESCPR2_DEB_FILE}" || apt-get install -y -f --no-install-recommends

    echo "[escpr2] installed version ${ESCPR2_VERSION} (${ARCH} prebuilt deb)"
    rm -rf /var/lib/apt/lists/*
    exit 0
fi

# ────────────────────────────────────────────────────────────────────
# 源码编译路径（arm64 等无预编译包的架构）
# ────────────────────────────────────────────────────────────────────
echo "[escpr2] arch=${ARCH} → no prebuilt deb, building from source"
echo "[escpr2] downloading from mirror ${ESCPR2_MIRROR_URL}"
download_or_skip "${ESCPR2_MIRROR_URL}" escpr2.tar.gz

mkdir src
cd src
tar xzf ../escpr2.tar.gz --strip-components=1
autoreconf -fi

# ──────────────────────────────────────────────────────────────────────
# 编译选项说明（修复 Debian trixie / GCC 15 上的编译错误）
# ──────────────────────────────────────────────────────────────────────
# ESCPR2 源码（Epson 闭源 + 开源混合体）写得相当随意，filter.c / mem.c 里
# 大量调用 `PrintBand` / `SendStartJob` / `SetupJobAttrib` / `err_system` 等
# 函数却**没有任何头文件声明**——实际定义在同 .c 里以 `epsPrintBand` /
# `epsStartJob` 等带前缀的形式存在，编译期 GCC 把它们当作隐式声明的外部函数
# 处理，依赖链接期符号兜底。
#
# 这种代码在 GCC 13 之前只是 warning，但：
#   ① C23 标准（GCC 15 在 trixie 上的默认 -std）把"隐式函数声明"和"隐式 int"
#      列为构造错误；
#   ② Debian trixie GCC 15 还在 default specs 里独立开启了
#      `-Werror=implicit-function-declaration` / `-Werror=implicit-int`，
#      所以单纯 `-std=gnu17` 回退语言标准也压不住——必须显式
#      `-Wno-error=implicit-function-declaration` 把它降级回 warning。
#
# AUR 维护的 epson-inkjet-printer-escpr2 PKGBUILD 也是同样思路（CFLAGS
# 加 -Wno-error=implicit-function-declaration），社区验证过的最小修复。
#
# 同时一并加上 `-Wno-error=incompatible-pointer-types`，因为 ESCPR2 内部
# 有 `unsigned char *` 与 `char *` 互传的老代码，C23 也对这类情况收紧了。
ESCPR2_CFLAGS="-O2 -std=gnu17 \
-Wno-error=implicit-function-declaration \
-Wno-error=implicit-int \
-Wno-error=incompatible-pointer-types"

export CC="gcc"
export CXX="g++"

./configure --prefix=/usr --disable-static \
    CFLAGS="${ESCPR2_CFLAGS}" \
    CXXFLAGS="-O2 -std=gnu++17"
make -j"$(nproc)"
make install

echo "[escpr2] installed version ${ESCPR2_VERSION}"
