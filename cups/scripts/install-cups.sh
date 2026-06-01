#!/usr/bin/env bash
# 从 OpenPrinting/cups 源码编译并覆盖安装 CUPS 到 /usr。
#
# 设计动机：见 cups/Dockerfile 顶部注释。简言之——
# cups-filters 会把 apt 版 cups 作为依赖拉进来，由它负责创建 lp/lpadmin 用户组、
# /etc/cups 目录骨架和 systemd unit 文件等；随后用源码编译出的二进制（同样
# --prefix=/usr）覆盖掉 apt 版的 libcups.so.2 / cupsd / cups-client 等文件，
# 既保留 Debian 侧的集成脚手架，又替换成 OpenPrinting 上游的最新版本，且
# libcups2 ABI 兼容让 cups-filters 和所有 printer-driver-* 可以继续工作。
#
# 注意：本脚本由 Dockerfile 调用，apt 装依赖、清理 *-dev 等工作不在这里做。

set -euo pipefail

# ────────────────────────────────────────────────────────────────────
# 配置（直接修改本文件即可升级版本，不通过 Dockerfile ARG）
# ────────────────────────────────────────────────────────────────────
# CUPS 版本：从 OpenPrinting/cups 的 GitHub Releases 拉取源码编译。
# 默认锁定到官方 2.4.x 稳定分支最新版——2.4.x 维持 libcups2 ABI 稳定，
# 与 Debian trixie 的 cups-filters 可直接链接复用；
# 3.x 系列已切换到 CMake 构建并移除了经典驱动模型（printer-driver-* 大量失效），
# 如需切到 3.x 必须同步改写下方的 configure/make 构建步骤。
# 版本来源：https://github.com/OpenPrinting/cups/releases
CUPS_VERSION="2.4.19"
CUPS_TARBALL_URL="https://github.com/OpenPrinting/cups/releases/download/v${CUPS_VERSION}/cups-${CUPS_VERSION}-source.tar.gz"
CUPS_TARBALL_FALLBACK_URL="https://codeload.github.com/OpenPrinting/cups/tar.gz/refs/tags/v${CUPS_VERSION}"

# ────────────────────────────────────────────────────────────────────
# 编译 & 安装
# ────────────────────────────────────────────────────────────────────
# configure 选项尽量贴齐 Debian 打包约定：
#   --prefix=/usr            覆盖 apt 版 /usr/bin/cupsd、/usr/sbin/cupsd 等；
#   --libdir=.../multiarch   Debian 多架构系统把 apt 版 libcups.so.2 装到
#                            /usr/lib/<triplet>/（ldconfig 里优先级高于 /usr/lib），
#                            如果我们用默认 libdir=/usr/lib 会被 apt 版遮蔽，
#                            所以显式用 dpkg-architecture 取 triplet 路径，
#                            让源码版 libcups/libcupsimage 彻底替换 apt 版；
#   --sysconfdir=/etc        沿用 /etc/cups 配置目录（cupsd.conf、ppd/、ssl/ 等）；
#   --localstatedir=/var     沿用 /var/{cache,log,spool}/cups 运行时目录；
#   --with-cups-user=lp      与 Debian cups-daemon 预置的系统用户一致；
#   --with-cups-group=lp     同上，避免 cupsd 启动时权限错乱；
#   --with-system-groups=lpadmin  管理员组，与 cups-daemon 预置保持一致；
#   --enable-libusb/--enable-avahi/--enable-dbus/--enable-gnutls
#                            开启 USB 直连、mDNS 发现、DBus 通知、TLS，
#                            对应容器内网络打印 + Airprint/Mopria 场景。
#
# 注：GitHub releases 的 tarball 已带 configure 脚本（发布前 bootstrap 过），
# 不需要再跑 autoreconf；若切到 master 分支 HEAD 源码需先 `./autogen.sh`。

BUILD_DIR="$(mktemp -d /tmp/cups-build.XXXXXX)"
trap 'rm -rf "${BUILD_DIR}"' EXIT

cd "${BUILD_DIR}"

echo "[cups] downloading ${CUPS_TARBALL_URL}"
if ! curl -fL --retry 5 --retry-all-errors --connect-timeout 20 --max-time 300 \
    -o cups.tar.gz "${CUPS_TARBALL_URL}"; then
  echo "[cups] primary download failed, fallback to ${CUPS_TARBALL_FALLBACK_URL}"
  curl -fL --retry 5 --retry-all-errors --connect-timeout 20 --max-time 300 \
    -o cups.tar.gz "${CUPS_TARBALL_FALLBACK_URL}"
fi
tar xzf cups.tar.gz --strip-components=1

MULTIARCH="$(dpkg-architecture -qDEB_HOST_MULTIARCH)"
echo "[cups] building for multiarch triplet: ${MULTIARCH}"

./configure \
    --prefix=/usr \
    --libdir="/usr/lib/${MULTIARCH}" \
    --sysconfdir=/etc \
    --localstatedir=/var \
    --with-cups-user=lp \
    --with-cups-group=lp \
    --with-system-groups=lpadmin \
    --enable-libusb \
    --enable-avahi \
    --enable-dbus \
    --enable-gnutls

make -j"$(nproc)"
make install

# 刷新动态链接器缓存，让后续步骤链接到源码编译出的 libcups.so.2
ldconfig

# 打印源码构建出的 CUPS 版本号，便于 CI 日志核对
/usr/sbin/cupsd -V || true

echo "[cups] installed version ${CUPS_VERSION}"
