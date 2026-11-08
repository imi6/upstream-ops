# UpstreamOps 外部分组倍率 API 调用文档

适用对象：需要在 NewAPI 后端读取上游分组倍率，并使用本地渠道 Key 匹配所属分组的开发者。

## 1. 适用版本与数据流

本文对应包含“密钥快照持久化、后台同步、显式强制刷新、脱敏 Key 兜底提示”功能的代码版本。部署前应确认运行镜像包含这些变更；接口能返回 HTTP 200，并不代表已部署新版。持久化版本包含 `key_sync_status`、`key_synced_at`；脱敏提示版本还包含 `key_hint_normalization`、`key_hints_complete` 等字段。

```text
后台任务 → 访问上游 → 保存倍率、密钥分组关系、Key 指纹与脱敏提示
NewAPI 后端 → 调用 UpstreamOps → 读取本地数据库中的快照
NewAPI 后端 → 优先匹配完整指纹 → 未命中时按完整候选范围兜底 → 展示倍率
```

- 普通外部请求只读 UpstreamOps 数据库，缺少快照时也不会自动访问上游。
- 密钥分组关系、SHA-256 指纹和脱敏提示存入独立表 `channel_api_key_snapshots`，不保存完整 Key。脱敏提示最多保留去除通用 `sk-` 后的前 3 位和后 4 位。
- 启用外部 API 后，启动或应用配置时会补齐未同步或不完整的数据；后续随“倍率扫描 Cron”同步允许的渠道，包含暂停监控的授权渠道。
- 默认倍率扫描 Cron 为 `13 */30 * * * *`，即每半小时的第 13 秒触发。实际周期以系统设置为准；关闭该 Cron 后，不再进行这项周期同步。
- 外部请求只有显式传入 `force_refresh=1` 或 `force_refresh=true`，才即时访问上游并更新数据库。
- 升级时自动扩展现有快照表；旧记录的脱敏字段在启动补齐或后续后台同步时写入。普通读取不会为补齐这些字段额外访问上游；需要立即更新时再显式强刷。

文中的渠道、分组、密钥 ID、倍率和时间均为演示数据，不代表实际生产数据。

## 2. 开通与鉴权

在“系统设置 → 外部分组倍率 API”中：

1. 启用外部 API，并配置独立访问令牌。
2. 配置允许读取的 UpstreamOps 渠道 ID；留空表示允许全部渠道。
3. 按需开启“仅此接口：仅显示已创建密钥的分组”。
4. 保存并应用配置。

请求地址示例：

```text
https://upos.zzshu.cc
```

所有接口都使用请求头鉴权，无需请求体：

```http
Authorization: Bearer <外部接口独立访问令牌>
Accept: application/json
```

必须区分三类密钥：

| 名称 | 用途 | 是否传给此接口 |
|---|---|---|
| UpstreamOps 外部接口访问令牌 | 授权 NewAPI 后端读取快照 | 是，仅放在 Authorization 请求头 |
| NewAPI 渠道配置中的上游 Key | NewAPI 本地计算指纹，必要时比对脱敏前后缀，匹配上游分组 | 否 |
| NewAPI 签发给终端用户的 Key | 用户调用 NewAPI | 否 |

外部令牌与 UpstreamOps 后台登录令牌相互独立。令牌至少 32 个可见 ASCII 字符，且不含空白；建议使用设置页面的生成按钮。不要将令牌放入浏览器前端、URL 查询参数、日志或代码仓库。

也可通过环境变量配置；非空环境变量优先于页面保存的配置：

```yaml
EXTERNAL_API_ENABLED: "true"
EXTERNAL_API_TOKEN: "替换为独立访问令牌"
EXTERNAL_API_CHANNEL_IDS: "1,3"
EXTERNAL_API_ONLY_CREATED_KEY_GROUPS: "true"
```

## 3. 接口与参数

### 3.1 批量查询授权渠道

```http
GET /api/external/v1/channel-rates
```

返回允许读取的渠道及其分组倍率。适合 NewAPI 后端定期拉取。

### 3.2 查询单个渠道

```http
GET /api/external/v1/channels/{channel_id}/group-rates
```

`channel_id` 是 **UpstreamOps 的渠道 ID**，不是 NewAPI 自己的渠道 ID。必须为正整数，并且在外部 API 授权范围内。

### 3.3 两个接口共用的查询参数

| 参数 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `group_name` | 字符串 | 不限制 | 按上游分组名称精确筛选，中文及特殊字符需要 URL 编码 |
| `group_id` | 正整数 | 不限制 | 按上游分组 ID 精确筛选；不带分组 ID 的记录不会匹配 |
| `only_with_keys` | 布尔值或 0/1 | 跟随系统设置 | `1` 强制仅返回已创建密钥的分组；不能关闭系统已开启的筛选 |
| `force_refresh` | 布尔值或 0/1 | `false` | `1` 即时刷新倍率和密钥快照，再返回结果 |

参数须为唯一非空值，重复参数或空值返回 `400`。同时提供 `group_name` 和 `group_id` 时，两项条件必须同时满足。

分组筛选只缩小返回内容，不缩小强制刷新范围：单渠道接口刷新整个指定渠道，批量接口刷新全部允许渠道。需要控制上游压力时，优先使用单渠道强制刷新。

兜底匹配须使用不带 `group_name`、`group_id` 的响应。这两个参数会让 `key_hints_complete=false`，避免将筛选后的少量 Key 当作完整候选集。“仅显示已创建密钥的分组”不会主动排除已有 Key；但其分组没有倍率快照时，兜底候选仍会被标记为不完整。

强制刷新也不保证所有上游读取都成功。部分指纹刷新失败时仍可能返回 HTTP `200`，必须继续检查 `key_sync_status`、`key_fingerprints_complete` 和 `unavailable_api_key_ids`。

本接口不接收上游 Key。即使只是传入 `api_key` 查询参数，也会返回 `400 unsupported_parameter`。

### 3.4 “仅显示已创建密钥的分组”规则

| 系统独立筛选开关 | 请求参数 | 实际返回范围 |
|---|---|---|
| 关闭 | 不传或 `only_with_keys=0` | 允许渠道中全部已采集的分组 |
| 关闭 | `only_with_keys=1` | 允许渠道中已创建密钥的分组 |
| 开启 | 不传、传 0 或传 1 | 始终仅返回允许渠道中已创建密钥的分组 |

该设置独立于渠道卡片原有的同名开关，不改变网关、通知设置等读取全部分组的行为。它限定的是下发候选范围，NewAPI 仍需根据自己的渠道 Key 做本地匹配。

## 4. 调用示例

### 4.1 Linux / macOS：curl

先设置环境变量，示例中的令牌需要自行替换：

```bash
export UPSTREAMOPS_BASE_URL="https://upos.zzshu.cc"
export UPSTREAMOPS_API_TOKEN="替换为外部接口令牌"

# 普通批量读取，只查询 UpstreamOps 数据库。
curl --fail-with-body --silent --show-error --max-time 40 \
  -H "Authorization: Bearer ${UPSTREAMOPS_API_TOKEN}" \
  -H "Accept: application/json" \
  "${UPSTREAMOPS_BASE_URL}/api/external/v1/channel-rates"
```

精确查询某渠道的一个分组：

```bash
curl --fail-with-body --silent --show-error --get --max-time 30 \
  -H "Authorization: Bearer ${UPSTREAMOPS_API_TOKEN}" \
  --data-urlencode "group_name=VIP 分组" \
  "${UPSTREAMOPS_BASE_URL}/api/external/v1/channels/7/group-rates"
```

强制刷新指定渠道：

```bash
curl --fail-with-body --silent --show-error --max-time 120 \
  -H "Authorization: Bearer ${UPSTREAMOPS_API_TOKEN}" \
  "${UPSTREAMOPS_BASE_URL}/api/external/v1/channels/7/group-rates?force_refresh=1"
```

批量强制刷新使用 `/api/external/v1/channel-rates?force_refresh=1`。建议客户端超时预留 180 秒，并确认反向代理允许较长响应时间。应用内部的单渠道强刷处理预算为 90 秒；批量强刷总预算为 120 秒、每渠道 45 秒，最多同时处理 4 个渠道。预算不是响应耗时保证，部分渠道可能失败。

### 4.2 Windows：PowerShell

```powershell
$baseUrl = "https://upos.zzshu.cc"
$token = "替换为外部接口令牌"
$headers = @{ Authorization = "Bearer $token"; Accept = "application/json" }

$result = Invoke-RestMethod -Method Get `
  -Uri "$baseUrl/api/external/v1/channel-rates" `
  -Headers $headers -TimeoutSec 40

# 检查整体完整性及渠道同步状态。
$result.data.complete
$result.data.channels |
  Select-Object channel_id, channel_name, only_with_keys, key_sync_status, key_synced_at

# 查看所有返回分组的倍率。
$result.data.channels | ForEach-Object {
  $channel = $_
  foreach ($group in $channel.groups) {
    [pscustomobject]@{
      渠道 = $channel.channel_name
      分组 = $group.group_name
      倍率 = $group.ratio
      采集时间 = $group.updated_at
      密钥记录数 = @($group.api_keys).Count
    }
  }
} | Format-Table -AutoSize

# 需要时单独调用；不要放进每次普通读取的流程中。
# Invoke-RestMethod -Method Get `
#   -Uri "$baseUrl/api/external/v1/channels/7/group-rates?force_refresh=1" `
#   -Headers $headers -TimeoutSec 120
```

## 5. 返回格式

### 5.1 批量成功响应

HTTP `200`：

```json
{
  "success": true,
  "data": {
    "generated_at": "2026-11-08T03:05:00Z",
    "complete": true,
    "channels": [
      {
        "channel_id": 7,
        "channel_name": "演示上游",
        "channel_type": "newapi",
        "site_url": "https://provider.example",
        "only_with_keys": true,
        "key_fingerprint_algorithm": "sha256",
        "key_fingerprint_normalization": "trim_strip_sk_prefix",
        "key_fingerprints_complete": true,
        "key_hint_normalization": "trim_strip_sk_prefix",
        "key_hints_complete": true,
        "unavailable_key_hint_ids": [],
        "key_synced_at": "2026-11-08T11:01:00+08:00",
        "key_sync_attempted_at": "2026-11-08T11:01:00+08:00",
        "key_sync_status": "ready",
        "unavailable_api_key_ids": [],
        "groups": [
          {
            "group_name": "VIP 分组",
            "description": "演示分组",
            "ratio": 0.8,
            "updated_at": "2026-11-08T11:00:00+08:00",
            "api_keys": [
              {
                "api_key_id": 123,
                "fingerprint": "2d83476aecc310cf5399c98a2483dac6e81eb3ea44812e1bdd0b1897299c088d",
                "masked_key": "sk-e1a...cac1",
                "key_prefix": "e1a",
                "key_suffix": "cac1"
              }
            ]
          }
        ]
      }
    ],
    "errors": []
  }
}
```

单渠道成功响应的结构为 `{"success":true,"data":{...渠道对象...}}`，其中 `data` 直接是上例 `channels` 中的一个对象，不包含 `generated_at`、`complete`、`channels` 或 `errors` 这些批量封装字段。

### 5.2 批量封装字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `success` | boolean | 请求处理成功，不代表所有渠道数据都完整 |
| `data.generated_at` | RFC 3339 时间字符串 | 本次响应生成时间，使用 UTC；不是上游数据采集时间 |
| `data.complete` | boolean | 本次允许渠道的读取及指纹快照是否完整；不代表倍率为实时值、包含所有上游分组或兜底候选完整，后者另看 `key_hints_complete` |
| `data.channels` | array | 成功读取的允许渠道；读取或强刷失败的渠道可能不在数组内 |
| `data.errors` | array | 渠道级错误，元素包含 `channel_id`、`code`、`message` |

`errors=[]` 仍可能伴随 `complete=false`，例如渠道已经返回，但其中某些 Key 指纹缺失。

### 5.3 渠道字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `channel_id` | integer | UpstreamOps 渠道 ID |
| `channel_name` | string | 渠道名称 |
| `channel_type` | string | 上游类型，当前为 `newapi` 或 `sub2api` |
| `site_url` | string | 上游站点标识；移除 URL 用户信息、查询参数、片段及末尾斜杠，主机名转小写 |
| `only_with_keys` | boolean | 本次是否启用“仅返回已创建密钥的分组” |
| `key_fingerprint_algorithm` | string | 当前固定为 `sha256` |
| `key_fingerprint_normalization` | string | 本地计算指纹前使用的归一化规则，见第 7 节 |
| `key_fingerprints_complete` | boolean | 密钥同步状态正常，且没有缺失或未能更新的指纹 |
| `key_hint_normalization` | string | 脱敏前后缀匹配的归一化规则，固定为 `trim_strip_sk_prefix`；与精确指纹规则独立 |
| `key_hints_complete` | boolean | 已成功同步密钥列表，最近一次列表同步未失败，所有 Key 都有至少 4 位可见尾号，且全部候选都包含在本次分组返回范围内；否则为 false |
| `unavailable_key_hint_ids` | integer array | 尚未回填脱敏信息、缺少可见尾号或尾号不足 4 位的密钥 ID；不随分组筛选变化 |
| `key_synced_at` | 时间字符串或 null | 最近一次密钥列表成功拉取并写入快照的时间；部分指纹失败时也可能更新 |
| `key_sync_attempted_at` | 时间字符串或 null | 最近一次同步尝试的记录时间 |
| `key_sync_status` | string | `ready`、`not_synced`、`partial` 或 `failed` |
| `key_sync_error` | string，可选 | 同步异常摘要；没有错误时省略，不包含上游登录凭据 |
| `unavailable_api_key_ids` | integer array | 无法提供有效指纹，或本次未能更新指纹的上游密钥 ID |
| `groups` | array | 经请求条件和系统设置筛选后的分组 |

密钥同步状态和缺失 ID 针对整个渠道，不会随 `group_name`、`group_id` 的返回筛选缩小范围。

`key_hints_complete` 独立于 `key_fingerprints_complete`：拿不到完整 Key 时，指纹可能缺失，但列表中的脱敏信息仍足够建立完整兜底候选。反过来，指纹完整也不保证有可用尾号。分组筛选、Key 缺少分组绑定或分组倍率尚未采集时，即使 `unavailable_key_hint_ids=[]`，`key_hints_complete` 仍可能为 false。该标志只覆盖当前允许读取的渠道与账号快照，不代表上游站点全部账号；管理员应授权 NewAPI 实际使用的完整候选范围。

### 5.4 分组及密钥字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `groups[].group_id` | integer，可选 | 上游分组 ID，通常用于 Sub2API；NewAPI 的记录通常不带此字段 |
| `groups[].group_name` | string | 上游分组名称，不一定与 NewAPI 本地分组同名 |
| `groups[].description` | string，可选 | 分组说明，空值时省略 |
| `groups[].ratio` | number | 上游分组倍率，例如 `0.8` 表示 0.8 倍 |
| `groups[].updated_at` | 时间字符串 | 该倍率最近一次成功采集的时间 |
| `groups[].api_keys` | array | 关联密钥的 ID、指纹与脱敏提示；可能为空，不包含完整密钥原文；指纹不可用的记录也会保留 |
| `groups[].api_keys[].api_key_id` | integer | 上游密钥 ID，不是 NewAPI 本地密钥或渠道 ID |
| `groups[].api_keys[].fingerprint` | string | 64 位小写十六进制 SHA-256 摘要；无法提供或指纹重复时为空字符串，不能把空字符串作为匹配值 |
| `groups[].api_keys[].masked_key` | string，可选 | 统一生成的脱敏展示值，例如 `sk-e1a...cac1`；上游未带 `sk-` 时也不添加，只有尾号时可为 `...cac1` |
| `groups[].api_keys[].key_prefix` | string，可选 | 去掉通用 `sk-` 后确定可见的前缀，最多 3 位；不是完整指纹的前缀 |
| `groups[].api_keys[].key_suffix` | string，可选 | 确定可见的尾号，最多 4 位；不是完整指纹的后四位；不足 4 位时不可自动兜底 |

脱敏列表支持 `*`、`...`、`…`、`•`、`●` 遮罩，只提取遮罩两端已知的字符。上游返回完整 Key 时，服务会先自行脱敏再保存；去除 `sk-` 后不足 16 位的完整 Key 不生成提示，避免暴露大部分内容。只有前缀而没有足够尾号的记录仍可展示提示，但不能用于自动兜底。

日期时间可能包含 UTC 或时区偏移，客户端应按 RFC 3339 解析。`ratio` 是上游倍率，不能直接当作价格、折扣百分比或 NewAPI 本地分组倍率；本文接口不自动修改 NewAPI 的计费配置。

## 6. 同步状态与部分失败

| `key_sync_status` | 含义 | NewAPI 处理方式 |
|---|---|---|
| `ready` | 已同步，指纹完整 | 使用匹配结果，同时展示倍率采集时间 |
| `not_synced` | 尚未建立完整的密钥列表快照 | 显示“尚未同步”，等待后台任务或由管理员显式强刷 |
| `partial` | 部分指纹缺失、未能更新或存在歧义 | 使用确认可用的部分，保留异常提示 |
| `failed` | 上次同步失败 | 有旧数据时保留显示并标注同步失败，没有数据时显示不可用 |

处理原则：

- 同步完整密钥列表失败时，保留上次快照；列表拉取成功后，快照会反映最新的新增、迁组和删除。
- 某个 Key 刷新失败时，服务可能保留仍可复用的旧指纹，并通过 `unavailable_api_key_ids` 标记。因此，能找到指纹不等于它已在本次成功更新。
- NewAPI 只能对正常返回、状态完整的对应数据范围做缓存删除或替换；不能因为一次部分失败就清空整批旧映射。
- `groups=[]` 可能是没有密钥、尚未同步，或者请求筛选没有匹配；必须结合同步状态判断。
- 指纹缺失或重复时 `api_keys` 仍保留记录，`fingerprint` 为空字符串；可复用的旧指纹仍可能非空，以 `unavailable_api_key_ids` 为准。`masked_key`、`key_prefix`、`key_suffix` 在可用时继续返回，不能因为存在脱敏提示就忽略指纹同步异常。
- `key_hints_complete=false` 或字段缺失时，禁止按“唯一候选”自动兜底；这不影响使用已确认有效的精确指纹。
- 未匹配结果应显示“未匹配”或“暂不可用”，不能默认为倍率 `1`。

普通请求即使发现旧数据、空数据或同步失败，也不会自动强刷。NewAPI 可以将解析后的倍率与同步状态保存在自己的业务缓存中，按 30～60 秒的周期普通读取；强刷留给明确的手动刷新操作。

## 7. NewAPI 本地 Key 匹配

### 7.1 匹配步骤

1. NewAPI 后端拉取授权渠道的快照，令牌不下发给浏览器。
2. 根据本地渠道的上游地址确定候选站点，收集响应中该站点的全部授权渠道。统一接入不需要逐渠道绑定；地址路径不能可靠对应时才显式配置站点或渠道映射，不能依赖名称相同。
3. 读取 NewAPI 渠道配置中已有的上游 Key，按响应中的归一化规则处理。
4. 对处理后字符串的 UTF-8 字节计算 SHA-256，并编码为小写十六进制。
5. 在同站点候选的 `groups[].api_keys[].fingerprint` 中精确匹配，忽略空指纹；结合 `unavailable_api_key_ids` 判断是否为旧数据。精确命中也必须唯一，多个命中不可取第一条。
6. 精确匹配没有命中时，才按第 7.3 节尝试脱敏前后缀兜底。精确匹配已经有歧义时，不得靠兜底消除歧义。
7. 读取唯一命中分组的 `ratio`、`group_name`、`updated_at`，同时结合同步状态决定是否标记旧数据；兜底命中另标记“近似匹配”。

归一化规则由 **UpstreamOps 返回的上游渠道类型** 决定，不是因为调用方是 NewAPI 就一律去掉 `sk-`：

| 规则 | 处理过程 |
|---|---|
| `trim` | 仅去除 Key 首尾空白，保留 `sk-` 前缀 |
| `trim_strip_sk_prefix` | 先去除首尾空白，再去掉一个开头的、区分大小写的 `sk-` 前缀 |

不改变 Key 大小写，不移除中间字符，不对十六进制摘要文本再次计算摘要。

### 7.2 Go 指纹计算示例

```go
package upstreamops

import (
    "crypto/sha256"
    "encoding/hex"
    "fmt"
    "strings"
)

// KeyFingerprint 仅在 NewAPI 后端计算，不向 UpstreamOps 上传上游 Key。
func KeyFingerprint(rawKey, normalization string) (string, error) {
    value := strings.TrimSpace(rawKey)
    switch normalization {
    case "trim":
        // 保留原有前缀。
    case "trim_strip_sk_prefix":
        value = strings.TrimPrefix(value, "sk-")
    default:
        return "", fmt.Errorf("不支持的指纹归一化规则：%s", normalization)
    }
    if value == "" {
        return "", fmt.Errorf("渠道 Key 不能为空")
    }
    digest := sha256.Sum256([]byte(value))
    return hex.EncodeToString(digest[:]), nil
}
```

第 5 节的演示指纹对应演示 Key `sk-e1a0123456789cac1` 使用 `trim_strip_sk_prefix` 后的结果：归一化为 `e1a0123456789cac1`，摘要为 `2d83476aecc310cf5399c98a2483dac6e81eb3ea44812e1bdd0b1897299c088d`。真实接入时使用自己的实际渠道 Key。

### 7.3 脱敏前后缀兜底

此部分由 NewAPI 后端实现；UpstreamOps 提供持久化提示和候选完整性标记，不接收本地渠道 Key，也不替 NewAPI 执行匹配。

1. 使用不带分组筛选的批量接口，收集同一上游站点下全部授权渠道；不能只在一个已选分组内寻找候选。若已明确限制某个账号或渠道范围，必须确保该范围包含 NewAPI 对应的全部可能 Key。
2. 同站点中任何渠道读取失败、缺失，或 `key_hints_complete` 不是 true，均停止自动兜底。批量 `data.complete=false` 可能仅因完整指纹缺失，不应单独用它否决脱敏候选；须结合 `data.errors` 与各渠道的 `key_hints_complete`。
3. 本地 Key 按 `key_hint_normalization` 处理：去掉首尾空白和一个开头的 `sk-`，保留大小写。此规则对所有上游的提示一致；Sub2API 的精确指纹仍使用 `trim`，不要混淆。
4. 候选的 `key_suffix` 必须有 4 位，本地 Key 必须以该尾号结尾；若还有 `key_prefix`，也必须以前缀开头。前缀不符时直接排除该候选，不能退回只比尾号。不要解析 `masked_key` 猜测中间字符，使用结构化字段比较。
5. 遍历完整候选集后，只有一个候选才显示对应倍率；零个显示“未匹配”，多个显示“匹配有歧义”。用 `(channel_id, api_key_id)` 标识候选；同一候选关联到多个不同分组或倍率时也不得自动选。
6. 兜底结果标注“近似匹配”。该结果只用于信息展示，不能作为鉴权、自动绑定凭据或自动修改计费配置的依据。

例如本地 Key 正文为 `e1a0123456789cac1`：提示 `e1a / cac1` 可命中，`e1b / cac1` 不命中；若另有仅尾号 `cac1` 的记录，它也属于候选，因此不能自动选中第一条。指纹后四位与 Key 后四位无对应关系，不能用指纹提取 `cac1`。

### 7.4 接入边界

接入边界：

- 多 Key 渠道须按 NewAPI 实际存储格式拆分后逐个匹配，不能把整段多 Key 配置一起计算摘要。
- 同一渠道的多个 Key 若命中不同分组或倍率，应逐 Key 展示，或明确制定汇总规则，不能直接取第一项。
- 同一上游站点可能对应多个 UpstreamOps 账号，不能随意挑一个账号做兜底。没有明确映射时，应合并该站点所有授权渠道再判断唯一性；候选冲突即停止自动匹配。
- 站点匹配应保留协议、有效端口和基础路径；NewAPI 地址可能额外包含 `/v1` 或自定义路径，不能只比较域名或盲目删除合法路径。无法确认地址映射时不自动兜底。
- 不使用分组显示名或不同系统的 ID 相等作为密钥匹配依据；脱敏尾号仅允许按第 7.3 节受限兜底。
- 旧版 UpstreamOps 没有脱敏字段时，NewAPI 保留原有精确指纹流程；新版旧记录尚未回填时等待后台同步或显式强刷，不从指纹推导前后缀。

## 8. HTTP 状态码与错误码

错误响应示例：

```json
{
  "success": false,
  "code": "unauthorized",
  "message": "访问令牌无效"
}
```

| HTTP 状态 | 错误码 | 含义 |
|---|---|---|
| `400` | `invalid_filter` | 参数重复、为空，或布尔值格式不正确 |
| `400` | `invalid_channel_id` | 渠道 ID 格式无效 |
| `400` | `invalid_group_id` | 分组 ID 不是正整数 |
| `400` | `unsupported_parameter` | 传入了禁止使用的 `api_key` 参数 |
| `401` | `unauthorized` | 缺少令牌、令牌格式错误或令牌无效 |
| `403` | `channel_forbidden` | 指定渠道不在允许读取范围内 |
| `404` | `api_disabled` | 外部 API 未启用 |
| `404` | `channel_not_found` | 已授权的指定渠道不存在 |
| `404` | `group_not_found` | 单渠道查询指定分组时没有匹配，包括尚未采集或被筛选排除 |
| `500` | `storage_error` | 查询渠道或渠道列表失败 |
| `502` | `channel_data_unavailable` | 单渠道本地倍率或密钥快照读取失败 |
| `502` | `refresh_failed` | 单渠道强制刷新失败；可去掉强刷参数读取已有本地快照 |
| `503` | `api_unavailable` | 外部 API 配置无效 |

批量查询中，单个渠道失败通常仍返回 HTTP `200`，并设置 `data.complete=false`、填充 `data.errors`，成功渠道继续返回。批量查询按分组筛选无匹配时，对应渠道可以保留在数组中且 `groups=[]`，不会像单渠道精确查询那样返回 `404 group_not_found`。

接口设置 `Cache-Control: no-store`，避免 HTTP 共享缓存保存携带鉴权的数据。调用方应同时检查 HTTP 状态、`success`、批量 `complete` 和各渠道的 `key_sync_status`，不要只判断 HTTP `200`。
