# Windows 版移植记录

目标：Windows 下载、安装、使用体验和界面与 macOS 版 DJOneHub 一致。

## 已在真机验证（2026-11-15，NUC i3-1115G4，Windows 11 Pro 26200）

- **AT 通道不需要第三方驱动。** 用 SetupAPI 给模块的 AT 接口强制选择 Windows 自带、
  微软签名的 `winusb.inf`（“WinUSB 设备”），由 `DiInstallDevice` 安装，不用重启。
  安装前在设备的 `Device Parameters` 写入 `DeviceInterfaceGUIDs`，
  DJOneHub 的 GUID 为 `{6d4f2b1a-8c3e-4f7a-9b21-d30e5a774c01}`。
  之后用 `CM_Get_Device_Interface_List` 找到路径，`CreateFile` + `WinUsb_Initialize`，
  批量端点读写 AT。安装需要管理员权限。
- **AT 接口编号随 usbnet 模式变化。** ECM（usbnet 1）下是 `MI_02`（端点 0x03/0x84），
  RNDIS（usbnet 3）下是 `MI_04`（端点 0x04/0x86）。模块重新枚举、接口类变化后，
  原来的绑定会失效，需要重新绑定。
- **上网用 RNDIS（usbnet 3）。** Windows 自带 `wceisvista.inf` 自动驱动，
  DHCP 拿到 192.168.225.x。两个模块网段相同，按源地址绑定可分别出网：
  模块 1 出口为 DITO，模块 2 出口为 AT&T，宽带默认路由不受影响。
  RNDIS 网卡的接口跃点（25、75）高于宽带（20），但正式版应主动调高，
  确保宽带始终是默认出口。
- ECM（usbnet 1）在 Windows 上没有自带驱动；模块插回 Mac 时，Mac 版会自动切回 ECM。
- WebView2 已预装（157），可作为界面外壳。

## 实现计划

1. 后端 Windows 支持（`cmd/djonehub-macos`）
   - `usbat_windows.go`：WinUSB 版 `usbAT`，接口与 `usbat_darwin.go` 一致
     （`Command`、`CommandWithPrompt`、`Description`、`Close`）。
   - WinUSB 绑定：模块接入时检查 AT 接口，没绑就绑。
   - `fleet_windows.go`：用 SetupAPI 枚举模块（位置、会话），实现网卡到模块的映射，
     开启 Windows 多模块（`multiModule && runtime.GOOS == "darwin"` 的限制要放开）。
   - 网卡发现：Windows 版 `discoverMacNetworkInterfaces`；`proxy_windows.go` 按模块限定网卡。
   - RNDIS 网卡跃点调高；`setKeepAwake` 已完成。
   - 后端以 Windows 服务（LocalSystem）运行：开机即启动，有绑定驱动所需的权限。
2. 界面：WebView2 外壳，按 SwiftUI 各页面一比一复刻（侧边栏、深色绿色主题、全部页面）；托盘图标、Windows 通知。
3. 安装包：安装服务和界面、开始菜单、开机启动、软件内自动更新；GitHub 发布增加 Windows 安装包。
4. 每一步在测试机上实测（`vps win1`）。

## 进度（2026-11-15）

- 后台：WinUSB AT、SetupAPI 识别模块、多模块进程、按模块绑定网卡的代理、网卡 DNS、跃点调整、Windows 服务（`--install-service`），均已在测试机实测。
- 界面：`cmd/djonehub-macos/web/app`，由后台在 `/app/` 提供；托盘程序 `cmd/djonehub-tray` 用 Edge 应用窗口打开它，并为新短信、来电弹通知。
- 安装包：`windows/DJOneHub.iss`，GitHub Actions 的 Windows 任务构建，随每个版本发布。
- 66：通话声音（模块 ADB 走 WinUSB，托盘程序用 winmm 把模块的 USB 音频接到电脑麦克风/扬声器，8 kHz，远端 3 kHz 四阶低通、近端回声抑制、静音、立体声录音）、来电铃声、一键更新（服务下载并校验安装包后静默安装，新服务在已登录会话里重新打开窗口）、WebView2 原生窗口、通讯录 vcf/csv 导入导出、设置页补齐。
- 模块 1（2ca3:4006）当前 USBCFG 的 ADB 位为 0，Mac 和 Windows 都需要先在 设置 › 通话支持 启用后才有通话声音。

