# EasyTier Access

独立的中文 EasyTier 人员与入网凭据管理面板。Go 标准库实现，网页通过 `go:embed` 嵌入二进制；运行无需 Python、Node、外部数据库或前端构建服务。

它管理的是 EasyTier **入网凭据**，不是官方 Web 后台账号，也不替代 EasyTier core。

## 已实现

- 单管理员登录、退出、会话校验和登录尝试限流。
- 监控看板：人员状态、有效及 7 天内到期凭据、待撤权与异常记录；支持搜索、筛选和自动同步。
- 节点状态：每 30 秒查询当前连接与中转路由，显示虚拟 IP、链路统计和凭据所属人员；不保留消失节点。
- 人员档案：姓名、部门、联系方式、备注、在职/撤权中/离职状态。
- 同步 EasyTier 现有凭据，补充设备名称并关联到人员。
- 签发独立设备凭据：有效期、ACL 分组、是否允许中转、是否允许复用、可宣告的子网。
- 设备启动选项：自动或固定虚拟 IPv4，可选通过日本节点作为 IPv4 VPN 出口；已有凭据可再次选择并生成启动命令。
- 两种启动方式：纯命令行、TOML 配置及配套启动命令；支持复制与下载 TOML。
- 一次性展示接入命令和凭据私钥；面板不保存、记录或再次展示私钥。
- 单条撤销、离职时批量撤权；部分失败保持“撤权中”，允许重试，不会误报全部成功。
- 恢复人员只恢复签发资格，不恢复已撤销凭据。
- 持久化操作记录，界面显示最近 200 条，数据文件保留最近 5,000 条。
- 中文桌面/手机界面、关键词搜索、状态过滤、错误状态和只在本机运行的独立演示模式。

## 先确认管理目标

1. 服务器必须已有与 core 配套的 `easytier-cli`，并支持：

   ```bash
   /你的路径/easytier-cli -p 127.0.0.1:15888 -o json credential list
   ```

2. 签发凭据的 core 必须是**你的业务网络的正式节点**，持有该业务网络的 `network_secret`，启用安全模式，并配置 `credential_file` 持久化凭据。相关设备和中间链路也需要兼容安全模式。

3. 如果日本节点使用此前的 `discovery-only` 配置，单纯帮助其他网络发现和中转，它不能签发其他业务网络的凭据。应让管理目标成为业务网络正式节点，或者把本面板部署在该业务网络已有的正式节点上。

4. `config.json` 的 `network_name` 必须与目标 core 的业务网络一致；它用于生成接入命令，不能通过修改它来切换 core 所属网络。`peer_url` 是设备实际可达的 EasyTier 入口，不是 frp 或网页端口。

5. 一个 RPC 地址若有多个实例，必须设置明确的 `instance_name`。面板会拒绝多实例 JSON 结果，避免一次操作落到多个网络。

完整接入流程：管理员添加人员 → 每台设备独立签发凭据 → 将接入命令交给对应使用者 → 离职时撤销其所有关联凭据。员工无需注册官方网页账号。

Core 的完整 TOML 参考 `deploy/core.example.toml`。将其中网络名和主密钥替换为现有业务网络的值，并确保 `credential_file` 所在目录可由 core 的运行用户写入。若需要访问日本节点本身的虚拟 IP，应保留原有虚拟 IP/TUN 配置，不使用示例中的 `no_tun=true`。

RPC 通过现有 core 的启动参数明确指定：

```bash
easytier-core -c /etc/easytier/core.toml --secure-mode true -r 127.0.0.1:15888
```

2.6.4 仅在 TOML 中设置安全模式可能没有初始化握手密钥；这里同时传入 `--secure-mode true`。应验证另一台测试节点能够完成安全握手，不能只用配置检查或凭据接口测试代替。自动密钥可能随重启变化，若客户端固定 peer_public_key，应另行持久化节点密钥。

将它合并进原有 systemd 的 `ExecStart`，不要重复启动第二个 core。`easytier-cli` 不需要作为服务运行，也不用 `-w` 接入官方 Web；面板按需执行它。

先以面板的运行用户验证 CLI 连接（服务部署时用户为 `easytier-access`）：

```bash
sudo -u easytier-access /usr/local/bin/easytier-cli \
  -p 127.0.0.1:15888 -o json credential list
```

收到有效 JSON（即使列表为空）后，在面板点击“同步节点”。仅 RPC 端口可达还不够；管理用户还必须能执行 CLI，目标实例也必须提供凭据管理服务。

## 下载后的快速启动

解压与你的服务器架构匹配的压缩包。普通 Intel/AMD VPS 使用 `linux-amd64`，ARM VPS 使用 `linux-arm64`。

```bash
chmod +x easytier-access
cp config.example.json config.json
```

编辑 `config.json`：修改 `cli_path`、`network_name`、`peer_url`；必要时修改 `rpc_portal` 和 `instance_name`。相对 `data_dir` 按配置文件所在目录解析。

初始化管理员，密码通过标准输入传递，不写进命令行参数（以下命令用于 Bash）：

```bash
read -r -s -p '设置至少 12 位的管理员密码：' ACCESS_ADMIN_PASSWORD
printf '\n'
printf '%s' "$ACCESS_ADMIN_PASSWORD" | ./easytier-access init-admin \
  --config config.json --username admin --password-stdin
unset ACCESS_ADMIN_PASSWORD

./easytier-access --config config.json
```

默认只监听 `127.0.0.1:8989`。在自己的电脑上建立 SSH 转发：

```bash
ssh -N -L 8989:127.0.0.1:8989 root@23.27.52.254
```

然后浏览器打开 <http://127.0.0.1:8989>，使用刚设置的管理员密码登录。Termius 也可配置相同的本地端口转发。

若通过反向代理公开访问，保留本机监听，将 `public_url` 改为最终 HTTPS 地址，并让代理保留 `Host`。该值同时用于来源校验和 Secure Cookie。不要把 EasyTier RPC 管理端口暴露到公网。

## 注册 systemd 服务

以下假设程序已放到 `/opt/easytier-access/`，命令需由有 sudo 权限的管理员执行。已有目录或配置应先检查；下列配置复制使用 `cp -n` 保留现有文件。

```bash
sudo useradd --system --no-create-home --shell /usr/sbin/nologin easytier-access
sudo install -d -m 0755 /opt/easytier-access
sudo install -d -m 0750 -o root -g easytier-access /etc/easytier-access
sudo install -d -m 0700 -o easytier-access -g easytier-access /var/lib/easytier-access
sudo install -m 0755 ./easytier-access /opt/easytier-access/easytier-access
sudo cp -n deploy/config.production.example.json /etc/easytier-access/config.json
sudo chown root:easytier-access /etc/easytier-access/config.json
sudo chmod 0640 /etc/easytier-access/config.json
```

编辑 `/etc/easytier-access/config.json`，并确保其中 CLI 路径对 `easytier-access` 用户可执行。systemd 服务启用了 `ProtectHome=true`，因此 CLI 不应放在 `/root` 或用户家目录；可把**配套版本**安装到 `/usr/local/bin/easytier-cli`。

初始化管理员（服务尚未启动）：

```bash
read -r -s -p '设置管理员密码：' ACCESS_ADMIN_PASSWORD
printf '\n'
printf '%s' "$ACCESS_ADMIN_PASSWORD" | sudo -u easytier-access \
  /opt/easytier-access/easytier-access init-admin \
  --config /etc/easytier-access/config.json --username admin --password-stdin
unset ACCESS_ADMIN_PASSWORD

sudo install -m 0644 deploy/easytier-access.service /etc/systemd/system/easytier-access.service
sudo systemctl daemon-reload
sudo systemctl enable --now easytier-access.service
sudo systemctl status easytier-access.service --no-pager
```

这只启动新面板，不会替换或重启已有的 EasyTier、frp、sing-box 服务。

## 演示模式

```bash
./easytier-access --demo
```

打开 <http://127.0.0.1:8989>，页面已填入演示账号 `admin` 和演示密码 `demo-access-2026`。演示模式只绑定本机，使用单独的 `data-demo/`；所有签发和撤销都是本地模拟，不会调用 CLI。示例不是服务器真实数据。

## 监控看板（1.1.0）

登录后默认进入“监控看板”，沿用现有人员档案和凭据，不需要修改 core 或 CLI，也不需要迁移数据库。

- 登记人员包括在职、撤权中、已离职人员；通过人员 ID 关联凭据，同名人员不会混合统计。
- 有效凭据按当前时间判断；“即将到期”包含未来 7 天内到期的有效凭据，包括第 7 天。
- “需处理人员”包括撤权未完成、关联凭据异常，以及离职后仍有有效凭据的人员。“在职但无有效凭据”单列提示，不自动当作故障。
- 待关联事项统计没有有效人员归属的有效或异常凭据；已到期、已撤销的历史记录不计入此待办。点击待办可进入对应筛选，进入凭据管理后可同时查看历史记录。
- 支持搜索姓名、部门、联系方式、设备名称及完整凭据 ID，按人员或授权状态筛选；“查看详情”可进入原有签发、撤销和离职处理流程。
- 看板可见时每 30 秒调用 CLI 同步一次；支持手动暂停，切换页面、隐藏浏览器标签或退出登录后停止自动请求。打开详情、弹窗或操作表格按钮时暂缓刷新，避免打断操作。后台刷新保留搜索、筛选和输入焦点。
- 同步失败保留上次快照并提示错误；超过 90 秒未成功同步时提示核实。未打开看板时不运行常驻采集器，也不记录流量历史。
- **授权有效不代表设备在线。** EasyTier 2.6.4 的凭据接口和节点接口没有可供面板可靠关联两者的凭据 ID，因此本版监控人员与授权状态，不提供人员在线判断，也不按设备名称猜测。

升级已有 systemd 部署时，使用对应架构的 1.1.0 二进制，保留原有 `config.json` 和数据目录；无需重新执行 `init-admin`。停止服务后替换二进制，再启动：

```bash
sudo systemctl stop easytier-access.service
sudo install -m 0755 ./easytier-access /opt/easytier-access/easytier-access
sudo systemctl start easytier-access.service
```

若你原先使用其他安装路径，应替换实际 `ExecStart` 指向的程序。网页资源已嵌入二进制，无需单独上传前端文件。

## 凭据与启动方式（1.1.0）

在“签发设备凭据”弹窗中选择：

1. **虚拟 IP 分配**：默认自动分配（`--dhcp true`）；选择固定 IP 时输入如 `10.10.10.20/24`，生成 `--ipv4 '10.10.10.20/24' --dhcp false`。固定 IP 凭据必须关闭多设备同时复用；面板会检查其他未失效凭据中已保存的相同固定 IP。面板不知道未登记设备的地址，请同时核对实际网络。
2. **通过日本节点上网（VPN）**：填写日本节点的 **EasyTier 虚拟 IPv4**，例如 `10.10.10.1`。不是日本节点的公网 IP `23.27.52.254`。关闭时生成普通内网接入命令。

启动选项只保存非敏感元数据，随凭据保留；不会变更凭据的 ACL、有效期，不会约束设备持有者修改启动 IP，也不会自动部署或重启客户端。

已有凭据可在“凭据管理”或人员详情中点击“启动命令”，通过下拉框选择其他有效凭据，调整启动选项：

- 留空“原凭据私钥”时，生成不含私钥的模板，使用终端变量 `EASYTIER_CREDENTIAL`。在 Bash/Zsh 中先执行 `read -r -s EASYTIER_CREDENTIAL`，粘贴原私钥并回车，再运行模板；执行完后可 `unset EASYTIER_CREDENTIAL`。
- 粘贴原私钥时，仅在当前浏览器内替换命令模板，不上传私钥，也不写入面板数据文件、操作日志或浏览器存储。关闭弹窗后清除临时结果；切换凭据会清空已输入私钥，避免误用。面板无法核对粘贴私钥是否属于该凭据。
- 已撤销、已到期、尚未确认有效，或归属停用人员的凭据不能生成启动命令。保存时会先同步节点核实。私钥遗失需要重新签发，无法从旧记录恢复。
- 命令使用 Bash/Zsh 的引用语法；Windows 用户可在 WSL 中运行，或将相同设置填入 EasyTier GUI，不能直接把单引号命令当作所有 Windows shell 通用语法。

启用 VPN 前，日本节点应设置其实际虚拟 IP，并在 core TOML 的现有 `[flags]` 中合并：

```toml
enable_exit_node = true
```

保持安全模式、网络身份、凭据持久化等原有配置。重启 core 后确认客户端能访问该虚拟 IP，且出口节点的 NAT/防火墙/ACL 允许相应流量。`allow_relay` 是让该凭据设备为其他节点中转的权限，和客户端选择日本出口是两个设置。

VPN 命令除了 `--exit-nodes '10.10.10.1'`，还包含 `--manual-routes '0.0.0.0/1' '128.0.0.0/1' --bind-device true`：前两条路由将 IPv4 上网流量导入 TUN，绑定物理接口用于避免 EasyTier 自身连接绕回隧道。仅选择出口节点不足以接管系统路由。

客户端需要管理员权限和可用 TUN；本版生成的是 **IPv4 VPN**。若要确保全部上网流量经过日本节点，需要在客户端停用未被接管的 IPv6，并检查 DNS 是否经过预期出口。运行后对比设备的公网 IPv4、DNS 和系统路由，不能用“成功生成命令”代替实际连通验证。本次本地验证覆盖官方 2.6.4 参数解析、签发/撤销和面板工作流，未在你的日本节点上验证实际公网出口。

## 命令行与 TOML 两种启动方式（1.2.0）

新签发的结果页，以及已有凭据生成结果页，均可切换“纯命令行 / TOML 配置”。两者使用同一组固定 IP / DHCP、VPN 出口等设置。

- **纯命令行**：复制全部参数的 `easytier-core ...` 命令，无需配置文件。
- **TOML 配置**：复制或下载 `client.toml`，再复制该页的配套启动命令。文件包含虚拟 IP、业务网络名、对等节点入口、安全模式、匹配的 X25519 公私钥，以及选中时的出口和路由配置。不要添加网络主密钥。

**EasyTier 2.6.4 的兼容要求**：仅执行 `easytier-core -c client.toml` 会在加载时将空网络主密钥归一化为普通节点身份，无法作为凭据节点完成认证。因此该版本必须配合 `--credential` 明确凭据身份：

```bash
easytier-core -c client.toml --credential '你的原凭据私钥'
```

界面会自动生成包含正确凭据的配套命令。不要额外添加 `--secure-mode true`，以免覆盖文件内的密钥。

已有凭据未粘贴原私钥时，提供的是 `client.template.toml`：私钥、公钥有明确替换标记，配套命令使用 `EASYTIER_CREDENTIAL` 变量。回到上一步粘贴真实凭据，可在浏览器内自动补齐私钥和匹配公钥；私钥不上传、不持久化。模板不能直接连接，演示凭据同样不能接入真实网络。

TOML 文件包含私钥，请保管好；Linux/macOS 可用 `chmod 600 client.toml` 限制访问。下载发生在当前浏览器，文件不会保存到面板服务器。复制配套命令仍使用 Bash/Zsh 语法。

已使用官方 EasyTier 2.6.4 在本机隔离环境验证生成配置的真实凭据握手；这不等同于日本节点的实际 VPN 公网出口测试。升级方法与 1.1.0 相同，替换二进制、保留配置和数据后重启，无需重新初始化管理员。

公钥派生脚本随 Go 二进制内嵌，来源为 `@noble/curves` 1.9.7，许可见 `THIRD_PARTY_LICENSES.txt`；无需浏览器访问外部 CDN。脚本重建说明见 `tools/README.md`，普通部署和 Go 构建无需安装 npm 依赖。

## Peer 节点与人员关联（1.4.0）

点击左侧“节点状态”立即查询，页面可见时每 30 秒同步一次，支持手动同步、暂停、节点/人员/IP/Peer ID 搜索以及连接、归属筛选。切换到其他页面、隐藏网页或退出登录后停止定时查询。

列表只保留最近成功查询中的远端连接与可用中转路由，不显示本机或“本次未发现”的历史记录；每次成功查询直接替换列表，升级后的首次成功查询会移除旧历史。直连记录必须在连接详情中仍有未关闭连接。同步失败或面板重启后暂不展示旧节点，提示重新查询；不会将无法核实的节点冒充在线。暂停或超过 90 秒未同步时，应参考同步时间和页面提示判断数据时效。

继续使用现有 CLI 和 RPC 配置，无需修改 core：

```bash
easytier-cli -p 127.0.0.1:15888 -o json peer
easytier-cli -p 127.0.0.1:15888 -o json -v peer
# 多实例 RPC 加 -n '实际实例名'
```

面板显示名称、Peer ID、版本、虚拟 IPv4/CIDR、NAT、协议、延迟、丢包、连接累计收发流量、同步时间，以及能识别出的人员与凭据。经中转表示 core 当前看到的可达路由，不代表与管理节点直接建立了握手连接。收发流量不是实时速率或人员用量，经中转节点可能没有对应链路统计。

**新签发凭据自动关联**：面板从一次性签发结果中派生 X25519 公钥，仅保存公钥和人员/凭据的对应关系。通过 `PeerVerified` 认证、身份为 `Credential`、网络匹配的直连连接，其公钥唯一匹配已登记凭据后，才显示人员、设备与凭据状态，并可点击人员查看详情。不会通过名称、IP、ACL 组猜测归属。共享主密钥节点、未认证连接、公钥冲突以及没有本地握手身份的中转节点，不会自动关联人员。

**旧凭据补录**：

1. 在“凭据管理 → 编辑归属 → 节点身份公钥”中填写该凭据实际对应的 `client.toml` 中的 `local_public_key`。也可在“用原凭据在本机提取”输入原私钥并点击“提取公钥”，私钥只在浏览器处理，不上传；保存时仅发送公钥。
2. 或在“节点状态”中，对已有认证公钥但未匹配的直连节点点击“关联人员”，选择设备实际使用的有效凭据和人员，核对公钥并确认。

旧凭据的对应关系由管理员核对并补录，标为“管理员补录”，接口不会直接证明该公钥对应哪个凭据 ID。请认真核对，避免人员归属和后续撤权指向错误凭据。已登记公钥不能被其他公钥覆盖，同一管理范围也不能将一个公钥关联到多个凭据。变更 RPC、实例或网络配置后，原范围的公钥记录不会用于新范围。

人员更名或归属调整会反映到节点列表。连接仍存在而凭据已撤销、过期或人员已离职时，仍显示对应状态；显示人员归属不代表凭据仍有效。识别的是凭据使用者的登记归属，凭据被转交时无法确认实际操作人的身份。签发响应丢失的记录需要补录公钥，或撤销后重新签发。

升级时停止面板，替换二进制后启动，保留原 `config.json` 和数据目录，无需重新初始化管理员。前端已嵌入二进制，刷新网页即可。

## 构建和验证

需要 Go 1.24+，没有第三方 Go 依赖。

```bash
go test -race ./...
go vet ./...
make dist
```

默认单元/HTTP 测试不接触真实节点。使用官方同机二进制执行隔离集成测试：

```bash
ET_TEST_CORE=/path/to/easytier-core \
ET_TEST_CLI=/path/to/easytier-cli \
go test -race -run TestNativeEasyTierCredentialLifecycle -v .
```

测试会在临时目录启动独立 core，只监听本机随机端口，不连接现有业务网络，验证真实的签发、JSON 同步、凭据握手、Peer 节点读取和撤销。

浏览器检查使用 Playwright 和 Chrome，测试脚本在 `tests/browser.cjs`。它会创建独立临时数据目录、自动启动本机演示并在结束时停止，不操作你的真实节点或现有演示数据：

```bash
go build -o .qa/easytier-access .
PLAYWRIGHT_MODULE=/path/to/node_modules/playwright node tests/browser.cjs
```

## 发布二进制到 GitHub Releases

发布包作为 Release 附件上传，`dist/` 继续由 Git 忽略，无需把二进制提交到源码仓库。

本机执行 `make release`，在 `dist/releases/` 生成 Linux AMD64、Linux ARM64、macOS ARM64 三个压缩包和 `SHA256SUMS-版本号`。每个包包含程序、说明、示例配置、部署文件和许可；不会包含运行数据或本地密钥。

仓库已配置 `.github/workflows/release.yml`。先更新 `main.go` 中的版本号、提交并推送，再创建对应标签，例如：

```bash
git tag v1.3.0
git push origin v1.3.0
```

GitHub Actions 会运行 Go 测试和静态检查，构建并上传附件，创建 **Release 草稿**。标签版本必须与程序版本一致，标签不能重复使用。到仓库的 Releases 页面检查草稿后点击 **Publish release**，下载者即可看到附件。GitHub 还会提供该标签对应的源码下载。

工作流使用 GitHub 自动提供的 `GITHUB_TOKEN`，无需额外保存个人 Token。也可手动上传本机生成的包：

```bash
gh release create v1.3.0 --target main --draft \
  --title 'EasyTier Access v1.3.0' --generate-notes \
  dist/releases/easytier-access-1.3.0-*.tar.gz \
  dist/releases/SHA256SUMS-1.3.0
```

自动流程和手动方式选择一种，同一标签不要重复创建 Release。

## 数据与操作边界

- 元数据和管理员密码哈希保存在 `data_dir/manager.json`，每次更新写临时文件、同步磁盘后原子替换；单进程文件锁避免两个面板覆盖同一目录。适合个人/小团队，不支持多副本同时写入。
- 面板不会保存凭据私钥。丢失一次性显示的私钥时，撤销后重新签发；core 自身的凭据持久化文件仍需按官方要求保管。
- “有效”表示最近同步时凭据有效，不等同于设备在线。CLI 当前列表不提供这里所需的可靠人员归属或设备在线状态，因此不伪造在线指示。
- “节点未发现”不冒充“已撤销”：可能是外部变更、实例选择或持久化变化，需要核实。
- 离职撤权只覆盖已关联到该人员的凭据；持有主密钥、复制了别人凭据、或使用其他网络的人，不能仅靠删除人员档案处理。
- `groups` 是凭据中的 ACL 身份标签。本面板没有改写其他设备 ACL；访问权限仍由目标设备的规则执行。
- 修改连接配置后重启本面板。更换管理员密码前先停止本面板，再执行 `init-admin` 并重启；管理员会话存于内存，重启失效。
- 备份时停止面板，再备份数据目录与配置文件；不要公开或提交这两项。文件含密码哈希和人员资料。

官方接口依据：

- <https://easytier.cn/guide/network/secure-mode.html>
- <https://github.com/EasyTier/EasyTier/blob/v2.6.4/easytier/src/easytier-cli.rs>
- <https://github.com/EasyTier/EasyTier/blob/v2.6.4/easytier/src/proto/api_instance.proto>
