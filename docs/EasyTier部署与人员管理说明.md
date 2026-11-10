# EasyTier 部署与人员管理说明

适用版本：EasyTier 2.6.4 与 EasyTier Access 1.0.0  
整理日期：2026 年 11 月 10 日

本文用于在日本 VPS 上配置 EasyTier 中转与凭据签发服务，并部署 Go 编写的人员管理面板。按第 2 至 7 节完成部署，再按第 8 节执行人员入网和离职撤权。第 9 至 14 节整理 ACL、官方 Web、仅发现模式、流量统计及故障处理。

主方案是让日本节点成为业务网络的正式节点，同时负责中转和凭据签发。员工每台设备使用独立凭据，不向员工分发业务网络主密钥。

## 1 服务关系与目录约定

管理链路：浏览器 → easytier-access → easytier-cli → 本机 core RPC。设备通过 EasyTier 的 11010 端口加入业务网络。管理网页、RPC、网络接入是三个不同入口。

| 程序 | 职责 | 本说明使用的入口 |
| --- | --- | --- |
| easytier-core | 组网、中转、安全模式、凭据管理 | TCP/UDP 11010；本机 RPC 15888 |
| easytier-cli | 按需执行管理命令，无需常驻 | 连接 127.0.0.1:15888 |
| easytier-access | 管理员登录、人员、签发、撤权 | Web TCP 8989 |
| frps | frp 端口映射，与面板独立 | 现有控制端口 7000、映射 7001 |
| sing-box | 代理服务，与面板独立 | 保留现有端口 |

日本节点 SSH：`root@23.27.52.254:22`。域名示例：`vps.obsidianlyg.top`。域名只负责解析，不能替代端口监听、防火墙或服务启动。

| 路径 | 内容 |
| --- | --- |
| /usr/local/bin/easytier-core | core 二进制 |
| /usr/local/bin/easytier-cli | 与 core 配套的 CLI |
| /etc/easytier/core.toml | core 配置，含业务网络主密钥 |
| /var/lib/easytier/credentials.json | core 持久化凭据文件，由 core 维护 |
| /opt/easytier-access/easytier-access | Go 面板二进制，已嵌入网页 |
| /etc/easytier-access/config.json | 面板配置，不填写主密钥 |
| /var/lib/easytier-access/manager.json | 人员、管理员密码哈希与操作记录 |

<!-- pagebreak -->
## 2 部署前检查与二进制安装

以下操作适用于 Linux 与 systemd。安装命令使用 sudo；以 root 登录时也可去掉 sudo。含 `read -s -p` 的密码命令请在 Bash 中执行。示例占位值必须替换，已有配置先备份。

### 2.1 先查清当前 core 的启动方式

```bash
uname -m
systemctl cat easytier.service
systemctl show easytier.service -p ExecStart -p User
/usr/local/bin/easytier-core --version
/usr/local/bin/easytier-cli --version
ss -lntup | rg '11010|15888|8989|7000|7001'
```

若服务器没有 rg，最后一条可改用 `grep -E`。普通 Intel/AMD VPS 选 linux-amd64，ARM64 服务器选 linux-arm64。core 和 CLI 应来自同一版官方发行包，本文按 2.6.4 说明。

如果已有服务名不是 `easytier.service`，后续用实际名称。若 ExecStart 使用 `-w`，说明配置来自官方 Web；先确认配置来源并导出、保存当前网络参数，再安排迁移。本说明后续采用本地 TOML 管理，不能直接拿示例覆盖 Web 管理的实例。

### 2.2 安装配套程序

从 EasyTier 官方 Releases 下载适合架构的 2.6.4 发行包，解压后进入包含 core 和 CLI 的目录。已有同版本程序可复用，无需重复安装。

```bash
sudo install -m 0755 ./easytier-core /usr/local/bin/easytier-core
sudo install -m 0755 ./easytier-cli /usr/local/bin/easytier-cli
sudo install -d -m 0700 /etc/easytier /var/lib/easytier
```

若要更新正在运行的二进制，先备份旧文件并停止对应服务，在维护窗口替换后启动；不要直接覆盖正在执行的文件。J4105 和相关业务节点也应确认兼容安全模式与凭据机制，优先统一版本。

面板发行包解压后包含 `easytier-access`、配置示例和 deploy 目录。后续面板安装命令均在该解压目录执行；不需要在 VPS 安装 Go、Python、Node 或数据库。

### 2.3 保留业务网络参数

记录当前 network_name、network_secret、peers、虚拟 IP、代理子网、ACL 和监听端口。已有网络继续使用原网络名和主密钥；本文的 `office` 只是示例。不要把现有 J4105 网络误改成另一个新网络。

<!-- pagebreak -->
## 3 配置 easytier-core

文件：`/etc/easytier/core.toml`。下面是纯中转与凭据签发节点的完整最小示例。凭据文件由 core 自动读写，不要手工创建空的 JSON 文件冒充有效数据。

```toml
hostname = "jp-authority"
listeners = ["tcp://0.0.0.0:11010", "udp://0.0.0.0:11010"]
credential_file = "/var/lib/easytier/credentials.json"

[network_identity]
network_name = "office"
network_secret = "REPLACE_WITH_YOUR_EXISTING_BUSINESS_NETWORK_SECRET"

[secure_mode]
enabled = true

[flags]
private_mode = true
no_tun = true
```

`network_name` 和 `network_secret` 必须与业务网络匹配。签发节点持有主密钥，员工通过单独签发的 credential 接入。只修改面板中的网络名，不会改变 core 所属网络。

`secure_mode.enabled=true` 开启安全模式。2.6.4 只写此项可能缺少本机握手密钥，下一节 ExecStart 必须同时包含 `--secure-mode true` 以初始化密钥；配置检查和 credential list 成功不足以证明握手正常。相关接入设备和中间链路也须兼容。`private_mode=true` 限制不受信任的外部网络使用节点，不能仅靠起一个复杂网络名阻止别人使用中转。

`credential_file` 保存签发信息，使凭据在 core 重启后可恢复。父目录必须由 core 服务的运行用户写入。本文 core 服务以 root 运行，因此目录权限 0700；若沿用非 root 服务，应按原服务用户设置属主。

`no_tun=true` 适用于只做中转和签发。如果还需要通过虚拟 IP 访问日本节点本身，应沿用原 IPv4、TUN 和路由配置，去掉此项或设为 false。不要同时丢掉原来的 peers、代理子网及 ACL。

### 3.1 检查配置

```bash
sudo chmod 0600 /etc/easytier/core.toml
sudo /usr/local/bin/easytier-core \
  --check-config -c /etc/easytier/core.toml
```

通过检查只代表配置可解析，仍需启动后确认端口、凭据接口及设备接入。RPC 地址在下一节通过 `-r` 明确指定，不依赖 TOML 中未经核实的同名字段。

<!-- pagebreak -->
## 4 注册 core 服务并验证 CLI

新安装可将以下内容保存为 `/etc/systemd/system/easytier.service`。若已有同名服务，先备份并修改原服务；只启动一套 core，避免端口冲突。

```ini
[Unit]
Description=EasyTier core relay and credential authority
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=/var/lib/easytier
ExecStart=/usr/local/bin/easytier-core -c /etc/easytier/core.toml --secure-mode true -r 127.0.0.1:15888
Restart=on-failure
RestartSec=3
UMask=0077
TimeoutStopSec=25

[Install]
WantedBy=multi-user.target
```

如果使用 `systemctl edit easytier.service` 的 drop-in 覆盖启动命令，必须先用空的 `ExecStart=` 清除旧值，再填写新的 ExecStart，并保留实际需要的原参数。systemd 内不要再加 `--daemon`。

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now easytier.service
# 已经运行的服务更新配置后还需执行：
sudo systemctl restart easytier.service
sudo systemctl status easytier.service --no-pager
sudo journalctl -u easytier.service -n 80 --no-pager
/usr/local/bin/easytier-cli -p 127.0.0.1:15888 \
  -o json credential list
```

成功标志是 CLI 返回单个实例的有效 JSON 对象；凭据列表为空也正常。RPC 仅绑定本机，不向公网开放 15888。CLI 不需要单独启动服务，不需要连接官方 Web，也不需要员工注册网页账号。

多实例时，CLI 用 `-n '实际实例名'` 选择；面板用 `instance_name` 选择。这里的实例名与业务网络名不是同一概念。仅端口连通不能证明目标实例支持 credential 接口。

<!-- pagebreak -->
## 5 配置面板 config.json

文件：`/etc/easytier-access/config.json`。下例按你要求监听 `0.0.0.0`，浏览器通过域名的 8989 端口访问。

```json
{
  "listen": "0.0.0.0",
  "port": 8989,
  "public_url": "http://vps.obsidianlyg.top:8989",
  "data_dir": "/var/lib/easytier-access",
  "cli_path": "/usr/local/bin/easytier-cli",
  "rpc_portal": "127.0.0.1:15888",
  "instance_name": "",
  "network_name": "office",
  "peer_url": "tcp://vps.obsidianlyg.top:11010",
  "cli_timeout": 15
}
```

| 配置项 | 含义与填写方法 |
| --- | --- |
| listen / port | 网页绑定地址和端口；0.0.0.0 表示所有 IPv4 网卡 |
| public_url | 浏览器实际使用的来源地址，协议、主机、端口须一致；不带子路径 |
| data_dir | 面板数据目录；systemd 部署优先使用绝对路径 |
| cli_path | CLI 绝对路径，面板用户必须能执行 |
| rpc_portal | 本机 core 管理入口，不是网页端口 |
| instance_name | 单实例可为空；多实例必须填写实际实例名 |
| network_name | 用于生成员工接入命令，必须匹配 core |
| peer_url | 员工实际可达的 EasyTier 入口，不能填 7001 或 8989 |
| cli_timeout | 单次 CLI 超时，单位秒，允许 1 至 120 |

访问网址是 `http://vps.obsidianlyg.top:8989`，不是 `http://0.0.0.0:8989`。如果改用 IP 访问，public_url 也要对应修改，否则可能返回 403。

公网长期使用建议配 HTTPS 反向代理：listen 可设为 127.0.0.1，public_url 改为最终 HTTPS 域名，代理保留 Host。HTTPS 时面板会启用 Secure Cookie。不要只把 public_url 改成 HTTPS 而没有配置 TLS 入口。

配置文件是严格 JSON，不写注释或末尾逗号。修改后重启面板。不要用 `--port` 临时覆盖生产配置，它还会将 public_url 改为本机地址。相对 data_dir 按配置文件所在目录解析。

<!-- pagebreak -->
## 6 安装面板并初始化管理员

在面板发行包的解压目录执行。下面命令保留已有 config.json；如果文件已存在，请编辑现有文件，不要直接覆盖。

```bash
id easytier-access >/dev/null 2>&1 || \
  sudo useradd --system --no-create-home \
    --shell /usr/sbin/nologin easytier-access
sudo install -d -m 0755 /opt/easytier-access
sudo install -d -m 0750 -o root -g easytier-access \
  /etc/easytier-access
sudo install -d -m 0700 -o easytier-access -g easytier-access \
  /var/lib/easytier-access
sudo install -m 0755 ./easytier-access \
  /opt/easytier-access/easytier-access
sudo cp -n deploy/config.production.example.json \
  /etc/easytier-access/config.json
sudo chown root:easytier-access /etc/easytier-access/config.json
sudo chmod 0640 /etc/easytier-access/config.json
```

将 config.json 编辑成第 5 节所需的值。发行包默认是本机监听，执行复制后不会自动变为 0.0.0.0。然后以面板用户验证权限和 RPC：

```bash
sudo -u easytier-access /usr/local/bin/easytier-cli \
  -p 127.0.0.1:15888 -o json credential list
```

`./easytier-access init-admin --password-stdin` 就是初始化管理员，但还需要从标准输入提供密码。正式部署请使用下面的完整命令，与服务使用相同用户、相同配置。执行前面板必须停止。

```bash
# 如已启动，先停止面板；无需停止 core：
# sudo systemctl stop easytier-access.service
read -r -s -p '设置管理员密码（至少12位）：' ACCESS_ADMIN_PASSWORD
printf '\n'
printf '%s' "$ACCESS_ADMIN_PASSWORD" | sudo -u easytier-access \
  /opt/easytier-access/easytier-access init-admin \
  --config /etc/easytier-access/config.json \
  --username admin --password-stdin
unset ACCESS_ADMIN_PASSWORD
```

真实环境没有默认密码。以后重置管理员也用同一命令，先停面板再重启；不要误用另一个 data_dir。不要把密码直接写入命令参数或脚本文件。

<!-- pagebreak -->
## 7 注册面板服务与首次访问

保存为 `/etc/systemd/system/easytier-access.service`，或使用发行包 deploy 目录内同名文件。

```ini
[Unit]
Description=EasyTier Access personnel and credential manager
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
User=easytier-access
Group=easytier-access
WorkingDirectory=/opt/easytier-access
ExecStart=/opt/easytier-access/easytier-access --config /etc/easytier-access/config.json
Restart=on-failure
RestartSec=3
StateDirectory=easytier-access
StateDirectoryMode=0700
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
TimeoutStopSec=25

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now easytier-access.service
sudo systemctl status easytier-access.service --no-pager
sudo journalctl -u easytier-access.service -n 80 --no-pager
ss -lntup | rg '11010|15888|8989'
```

服务启用了 ProtectHome，所以 CLI 应放在 `/usr/local/bin`，不要放 `/root` 或用户家目录。启动 Go 面板无需单独上传 static 文件夹，网页已嵌入二进制。

按云防火墙和主机防火墙实际情况，允许设备入口 TCP/UDP 11010；直连网页时放行 TCP 8989。不要开放 RPC 15888。浏览器打开第 5 节的 public_url，用刚初始化的管理员登录，点击“同步节点”。

若选择本机监听方案，public_url 同步改为 `http://127.0.0.1:8989`，在自己电脑建立 SSH 转发后访问该地址：

```bash
ssh -N -L 8989:127.0.0.1:8989 root@23.27.52.254
```

<!-- pagebreak -->
## 8 人员入网与离职撤权

### 8.1 管理员签发

1. 登录面板，先同步节点，确认目标实例连接正常。
2. 新增人员，填写姓名、部门、联系方式和备注。
3. 为这个人的每台设备分别签发凭据，设备名写清用途，例如“张三工作电脑”。
4. 设置有效期，当前面板支持 1 至 365 天；按需要填写 staff 等 ACL 分组。
5. 普通终端通常关闭“允许中转”和“允许复用”；只有确有需求时启用。
6. 将一次性展示的接入命令安全交给对应员工。员工使用兼容版本的 EasyTier 执行或填入相应客户端配置。

面板生成的命令形式如下。实际凭据应使用签发时展示的完整值，不是 credential_id。

```bash
easytier-core --network-name 'office' \
  --secure-mode --credential 'REPLACE_WITH_ISSUED_CREDENTIAL_SECRET' \
  -p 'tcp://vps.obsidianlyg.top:11010'
```

此命令表示认证和接入方式；需要虚拟 IP、DHCP、子网代理或开机启动时，再按设备原有配置补充，不要删除原有必需参数。接入后用实际业务访问验证，凭据签发成功不等于业务路由和 ACL 都已正确配置。

私钥只显示一次，面板不保存、不记录、不能再次查看。丢失时撤销旧凭据后重新签发。“允许中转”控制该凭据节点作为转发节点的能力，不是给员工创建官方网页管理员账号；“允许复用”涉及多节点并发使用同一凭据。

### 8.2 离职与异常处理

先同步并核对该人员的全部设备和已导入凭据，再执行离职撤权。面板逐条请求 core 撤销关联的有效凭据；部分失败会保留“撤权中”，排除故障后重试，不能把失败视为完成。

撤权后再次同步，并实际检查旧设备的接入与业务访问。恢复人员只恢复签发资格，不恢复已撤销凭据。若设备用过其他人的凭据，或人员曾拿到主密钥，应额外处理这些凭据，必要时更换主密钥并更新合法节点。

“有效”表示最近同步时凭据仍有效，不表示设备在线。“节点未发现”也不等于确认撤销，可能与实例选择、持久化或外部变更有关。面板人员清单、凭据清单、网络在线节点是不同视图。

<!-- pagebreak -->
## 9 ACL 与官方 Web 常见问题

### 9.1 ACL 到底按什么判断

ACL 是网络访问规则，决定哪些来源可以访问哪些目标、端口和协议。它依据规则中的设备分组、IP 或网段等条件匹配，不会自动把“人员姓名”变成授权条件。

可以把人员“张三”的两台设备分别签发成 staff 分组，再在提供业务服务的设备上配置规则，允许 staff 访问指定资源。凭据给设备身份标签，ACL 负责流量准入；只在面板填写 staff，不会自动生成或下发目标设备的 ACL。

ACL 应在相关目标节点按官方格式配置并验证，必要时同时限制转发路径。本人员面板没有 ACL 编辑和全网下发功能。不要把安全模式凭据分组与旧式共享主密钥下的分组口令混为一谈。

### 9.2 有人离职需要删除什么

人员记录用于归属管理；真正阻止继续使用其入网身份，需要撤销对应的设备凭据。只删网页账号、移出某个列表或改人员名字，都不能替代撤销。

每台设备独立签发，避免多人共用凭据。即使配置了 ACL，如果员工知道 network_secret，也可能以其他身份重新接入；这时要更换已泄露的主密钥及其他受影响密钥，不能只撤销某一条凭据。

### 9.3 easytier-web-embed 有什么用

它提供官方 Web 管理界面及相应服务，用来管理主动注册到该配置服务的机器。它本身不能替代 easytier-core 的组网和中转，也不会因为某台设备连接了日本中转，就自动把该设备纳入你的管理账号。

官方网页只看到当前设备时，先区分“设备注册到配置服务”与“设备加入业务网络”。其他机器需要正确连接相应配置服务和管理账号，才会进入其管理视图。已有 `-w` 部署应沿用对应配置链路检查。

### 9.4 官方网页能管理 credential list 吗

针对这里使用的官方 2.6.4 Web 前端，不以其具备完整的 credential 列表、签发和撤销页面为前提。GitHub 开发分支的界面与已发布版本可能不同。

本次 Go 面板专门补充管理员签发、撤销和人员归属管理，直接通过本机 CLI 操作 core。员工无需注册官方网页账号。它也没有员工自行申请入网、多管理员或官方 Web 账号管理功能。

<!-- pagebreak -->
## 10 私有中转与仅发现模式

### 10.1 只让自己的人使用中转

采用第 3 节主方案：日本节点属于业务网络，开启安全模式与 private_mode，管理员保管主密钥，员工通过独立凭据接入。监听端口可以公开可达，访问资格由身份验证控制；不要把“开放端口”等同于“任何人都能使用”。

### 10.2 只做发现不转发业务流量

这是另一种部署目标，不能直接与主方案混在同一份配置中。下面用于帮助外部业务网络发现对端，节点身份故意与业务网络分开。

```toml
hostname = "jp-discovery"
listeners = ["tcp://0.0.0.0:11010", "udp://0.0.0.0:11010"]

[network_identity]
network_name = "discovery-only"
network_secret = "REPLACE_WITH_SEPARATE_DISCOVERY_SECRET"

[flags]
no_tun = true
private_mode = false
relay_network_whitelist = ""
relay_all_peer_rpc = true
```

`relay_network_whitelist` 这里是空字符串，不是数组。空白名单限制外部网络的业务中转，relay_all_peer_rpc 则保留帮助发现和打洞的控制消息。节点自身所属网络可能仍有转发例外，因此不能拿这个配置当作同业务网络绝对不转发的保证。

仅发现仍有连接、心跳和控制流量；直连失败时也没有这个节点提供业务中转兜底。它不持有业务网络主密钥，不能为其他业务网络签发凭据。若还要做人员凭据管理，面板应连接业务网络内的正式签发节点。

### 10.3 能不能把 J4105 的 EasyTier 用 frp 映射出来

可以将 EasyTier 的 TCP 或 UDP 监听通过对应 frp 类型映射成公网入口；转发协议、端口、peer_url 和 mapped listener 公告需要匹配。但业务数据会经过 frp 和 VPS，增加路径与维护环节，吞吐和延迟也受 VPS、frp 和本地上行限制。

你已在日本节点部署 core，直接让它做入口和中转通常更清晰。现有 `7001 → J4105:8080` 是另一条 Web 映射链路，不要把它填成 EasyTier 的 peer_url。

<!-- pagebreak -->
## 11 流量统计与资源开销

EasyTier、frp、sing-box 的空闲心跳通常较小，主要流量来自实际转发。一次经过 VPS 的约 1 GB 业务数据，可能形成约 1 GB 接收加 1 GB 发送，另有协议开销；账单按出站还是双向统计以服务商规则为准。同机运行三个服务不会自动产生三倍流量。

低内存的整机历史统计可选 vnStat，按公网网卡统计；按服务拆分可使用 systemd IPAccounting。后者依赖内核、cgroup 和 BPF 支持，通常开销较低，高包速时仍会增加 CPU 工作，不能保证固定比例或完全无开销。

```bash
sudo systemctl set-property easytier.service IPAccounting=yes
sudo systemctl set-property frps.service IPAccounting=yes
sudo systemctl set-property sing-box.service IPAccounting=yes
```

检查 `systemctl show 服务名 -p IPAccounting` 并观察真实业务时计数是否增长；部分环境可能需要重启服务后生效。以实际单元名为准，未安装的单元从列表删除。以下脚本按“服务名在上，流量在下，服务间空一行”显示。

```bash
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
```

本脚本使用十进制 MB，即 1 MB = 1,000,000 字节。systemd 计数随服务运行周期重置，不提供月度历史；可能含本机流量，服务间转发也可能重复计数，所以相加不一定等于云厂商账单。vnStat 的公网网卡历史统计用于长期总量更合适。

<!-- pagebreak -->
## 12 按症状排查问题

### 12.1 网页无法打开或返回 403

先看 `systemctl status easytier-access.service` 和日志，再用 ss 确认 8989 的绑定地址。若只绑定 127.0.0.1，公网无法直连；如使用 0.0.0.0，还要检查云安全组、主机防火墙和域名解析。

403 时核对 public_url 与浏览器地址的协议、域名或 IP、端口；反向代理应保留 Host。不要通过 7001 测试 8989 面板，不要把 0.0.0.0 当成访问地址。更换访问方式后清理旧页面并重新登录。

若日志出现 `local private key is not set`，检查实际 ExecStart 是否带 `--secure-mode true`；按第 4 节修正后 daemon-reload 并重启 core。自动生成的节点密钥可能随重启改变；已使用 peer_public_key 固定身份时，应另行固定并持久化节点密钥。

### 12.2 面板无法同步或签发

先运行以下命令，区分 core 连接故障与面板权限故障：

```bash
sudo journalctl -u easytier.service -n 80 --no-pager
/usr/local/bin/easytier-cli -p 127.0.0.1:15888 \
  -o json credential list
sudo -u easytier-access /usr/local/bin/easytier-cli \
  -p 127.0.0.1:15888 -o json credential list
```

root 成功、面板用户失败时，检查二进制权限、路径及 ProtectHome。二者都失败时，检查 core 是否启动、安全模式、RPC 地址、版本是否配套，以及是否连错实例。提示 invalid service 时，还要检查目标 core 是否提供凭据接口，不能只看端口是否开放。

CLI 返回多个实例结果时填写 instance_name。签发或撤销超时后先同步核实实际结果，再决定重试，避免把“结果未确认”误判成“肯定没执行”。

### 12.3 管理员初始化失败或重启后数据变化

“数据目录正在被另一面板进程使用”表示文件锁生效；先停止面板。权限错误则确认服务用户对 data_dir 可写。初始化和启动必须用相同 config.json，不能一个写相对 data，一个写 `/var/lib/easytier-access`。

core 重启后凭据丢失时检查 credential_file 是否配置、文件是否存在及目录权限。面板 manager.json 与 core 凭据文件是两套数据，不能用其中一份替代另一份。

### 12.4 frp 的 7001 返回 502

单独验证三段链路：J4105 本机 8080 是否正常、frpc 是否连接当前日本 IP 的控制端口、frps 是否在 7001 监听。此前域名更换 IP 后，frpc 的长连接可能仍保持旧地址；核对日志并在维护时重连 frpc。DNS 解析正确不能证明旧连接已迁移，也不能证明本地 8080 服务正常。

<!-- pagebreak -->
## 13 日常维护与备份恢复

### 13.1 常用维护命令

```bash
sudo systemctl restart easytier-access.service
sudo journalctl -u easytier-access.service -f
sudo systemctl restart easytier.service
sudo journalctl -u easytier.service -f
```

只修改面板 config.json 就重启面板；修改 core.toml 才重启 core。修改 unit 文件后先 daemon-reload，再重启对应服务。停止或重启面板不会自动停止 EasyTier 业务转发，core 重启则会影响组网和中转。

手工查看、签发与撤销示例：

```bash
easytier-cli -p 127.0.0.1:15888 -o json credential list
easytier-cli -p 127.0.0.1:15888 -o json credential generate \
  --ttl 604800 --groups staff --reusable false
easytier-cli -p 127.0.0.1:15888 -o json credential revoke \
  REPLACE_WITH_CREDENTIAL_ID
```

TTL 单位是秒，604800 为 7 天。手工签发的凭据需要在面板同步后补充人员归属，私钥输出也应按敏感信息保管。多实例命令补充 `-n '实际实例名'`。

### 13.2 必须同时保存的内容

备份 core.toml、core 凭据目录、面板 config.json、面板数据目录，以及实际生效的 systemd unit 和 drop-in。凭据私钥没有存于面板，不能指望从面板备份恢复一次性显示的接入私钥。

为取得一致备份，在维护窗口停止面板和 core 后复制，再恢复服务。停止 core 会短暂中断业务。下面将四个配置与数据目录归档到 root 私有目录；unit 文件和 drop-in 按实际部署另存。

```bash
sudo install -d -m 0700 /root/easytier-backups
sudo systemctl stop easytier-access.service easytier.service
sudo bash -c 'umask 077; tar -czf \
  /root/easytier-backups/backup-$(date +%Y%m%d-%H%M%S).tar.gz \
  /etc/easytier /var/lib/easytier \
  /etc/easytier-access /var/lib/easytier-access'
sudo systemctl start easytier.service easytier-access.service
```

确认归档成功后，离机保存到受控位置。恢复时先停服务，保留现有数据副本，恢复相同版本的程序和配置，修正属主权限，再启动 core 和面板。恢复较旧的凭据文件可能回退撤销状态，必须重新核对离职与撤销记录，并补做撤权。

<!-- pagebreak -->
## 14 上线验收与参考资料

### 14.1 按顺序验收

1. core 与 CLI 版本配套，core.toml 校验通过，现有网络参数完整保留，启动参数包含 --secure-mode true。
2. core 服务运行正常，11010 对需要的设备可达，RPC 只监听 127.0.0.1:15888。
3. 面板用户执行 credential list 能得到目标实例的单个 JSON 对象。
4. config.json 的 network_name 与 core 一致，peer_url 指向 EasyTier 入口。
5. 管理员初始化成功，面板使用同一配置和数据目录启动，public_url 对应实际访问地址。
6. 面板同步成功，创建测试人员并为测试设备签发独立凭据。
7. 测试设备加入网络，按目标 ACL 验证业务访问；不要只看网页状态。
8. 执行测试人员离职，核对全部关联凭据已撤销，并验证旧设备访问结果。
9. 在可接受的维护窗口验证重启后的持久化和备份恢复流程，保留现有服务的回退方案。

### 14.2 使用边界

当前面板是单管理员、单写入进程的人员与凭据管理工具，不是员工自助申请系统、全网在线设备监控器或 ACL 配置平台。管理员会话保存在内存，面板重启后需重新登录。

正式部署时使用真实初始化的管理员账号；`--demo` 只做本机模拟，数据在独立 data-demo 目录，不会调用实际 CLI。演示中的“签发成功”不能作为服务器联调结果。

文中使用的配置值、路径和端口组成一套可执行的部署约定。复用已有服务时，按实际启动路径、用户、端口及配置来源调整；所有 REPLACE_WITH 开头的值都必须替换。配置、凭据文件、备份和日志中可能包含敏感信息，应限制读取权限。

### 14.3 官方资料

- EasyTier 2.6.4 发行版： https://github.com/EasyTier/EasyTier/releases/tag/v2.6.4
- 安全模式与凭据： https://easytier.cn/guide/network/secure-mode.html
- ACL 配置： https://easytier.cn/guide/config/acl.html
- 公共中转与发现： https://easytier.cn/guide/network/host-public-server.html
- 官方 Web 控制台： https://easytier.cn/guide/network/web-console.html
- 2.6.4 CLI 源码： https://github.com/EasyTier/EasyTier/blob/v2.6.4/easytier/src/easytier-cli.rs

网页文档可能随版本更新。实际部署以 2.6.4 的命令帮助、配置检查和运行结果为准；不要把开发分支功能直接当成已安装版本的能力。
