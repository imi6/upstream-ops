import { useEffect, useState } from "react";
import { toast } from "sonner";
import {
  Bell,
  Clock3,
  MonitorCog,
  KeyRound,
  Network,
  PencilLine,
  Plus,
  RefreshCw,
  Send,
  Server,
  ShieldCheck,
  Trash2,
} from "lucide-react";
import { Card, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useConfirm } from "@/components/ui/confirm-dialog";
import { CaptchaFormDialog } from "@/components/monitor/captcha-form-dialog";
import { NotificationFormDialog } from "@/components/monitor/notification-form-dialog";
import { apiFetch } from "@/lib/api";
import { copyText } from "@/lib/clipboard";
import { useTriggerRefresh } from "@/lib/refresh-context";
import type {
  AppVersion,
  ApplyConfigResult,
  CaptchaConfig,
  NotificationChannel,
  NotificationChannelType,
  SystemConfig,
} from "@/lib/api-types";
import { decimal, money, relativeTime } from "@/lib/format";
import {
  useCaptchaConfigs,
  useChannels,
  useNotificationLogs,
  useNotificationChannels,
  useAppVersion,
  useSystemConfig,
} from "@/lib/queries";
import { cn } from "@/lib/utils";

function num(v: string) {
  return Number(v || 0);
}

interface ProxyTestResult {
  ok: boolean;
  latency_ms: number;
  ip: string;
  provider: string;
  error?: string;
}

export default function SettingsPage() {
  const query = useSystemConfig();
  const channels = useChannels();
  const notifications = useNotificationChannels();
  const captchas = useCaptchaConfigs();
  const notificationLogs = useNotificationLogs(1, 10);
  const appVersion = useAppVersion();
  const refresh = useTriggerRefresh();
  const { confirm, dialog: confirmDialog } = useConfirm();
  const [form, setForm] = useState<SystemConfig | null>(null);
  const [externalAPIToken, setExternalAPIToken] = useState("");
  const [externalChannelIDs, setExternalChannelIDs] = useState("");
  const [saving, setSaving] = useState(false);
  const [applying, setApplying] = useState(false);
  const [configSavedPendingApply, setConfigSavedPendingApply] = useState(false);
  const [testingProxy, setTestingProxy] = useState(false);
  const [checkingVersion, setCheckingVersion] = useState(false);
  const [editingNotification, setEditingNotification] =
    useState<NotificationChannel | null>(null);
  const [notificationOpen, setNotificationOpen] = useState(false);
  const [editingCaptcha, setEditingCaptcha] = useState<CaptchaConfig | null>(
    null,
  );
  const [captchaOpen, setCaptchaOpen] = useState(false);
  const [busyNotificationID, setBusyNotificationID] = useState<number | null>(
    null,
  );
  const [busyCaptchaID, setBusyCaptchaID] = useState<number | null>(null);
  const [activeTab, setActiveTab] = useState("system");
  const [versionInfo, setVersionInfo] = useState<AppVersion | null>(null);

  useEffect(() => {
    if (query.data?.config) {
      const externalAPI = {
        ...(query.data.config.externalAPI ?? {
          enabled: false,
          tokenConfigured: false,
          channelIDs: [],
        }),
        onlyCreatedKeyGroups: query.data.config.externalAPI?.onlyCreatedKeyGroups ?? false,
      };
      setForm({ ...query.data.config, externalAPI });
      setExternalChannelIDs(externalAPI.channelIDs.join(", "));
      setExternalAPIToken("");
    }
  }, [query.data]);

  useEffect(() => {
    if (appVersion.data) {
      setVersionInfo(appVersion.data);
    }
  }, [appVersion.data]);

  if (query.loading && !form) {
    return (
      <section className="text-sm text-muted-foreground">加载配置中...</section>
    );
  }

  if (query.error && !form) {
    return (
      <section className="text-sm text-destructive">{query.error}</section>
    );
  }

  if (!form) return null;

  const recentLogs = notificationLogs.data?.items ?? [];
  const lastSent = recentLogs[0]?.sent_at ?? null;
  const recentFailed = recentLogs.filter((item) => !item.success).length;

  async function handleDeleteNotification(channel: NotificationChannel) {
    const ok = await confirm({
      title: `删除通知渠道 ${channel.name}？`,
      description: "删除后该渠道将不再接收系统通知。",
      confirmLabel: "删除",
      destructive: true,
    });
    if (!ok) return;
    setBusyNotificationID(channel.id);
    try {
      await apiFetch(`/notifications/channels/${channel.id}`, {
        method: "DELETE",
      });
      toast.success(`已删除 ${channel.name}`);
      refresh();
      notifications.refetch();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "删除失败");
    } finally {
      setBusyNotificationID(null);
    }
  }

  async function handleTestNotification(channel: NotificationChannel) {
    setBusyNotificationID(channel.id);
    try {
      const res = await apiFetch<{ ok: boolean; error?: string }>(
        `/notifications/channels/${channel.id}/test`,
        {
          method: "POST",
        },
      );
      if (res.ok) {
        toast.success(`已发送测试消息到 ${channel.name}`);
      } else {
        toast.error(res.error ?? "测试失败");
      }
      refresh();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "测试失败");
    } finally {
      setBusyNotificationID(null);
    }
  }

  async function handleDeleteCaptcha(item: CaptchaConfig) {
    const ok = await confirm({
      title: `删除验证码服务 ${item.name}？`,
      description: "删除后引用此服务的渠道需要重新指定验证码服务。",
      confirmLabel: "删除",
      destructive: true,
    });
    if (!ok) return;
    setBusyCaptchaID(item.id);
    try {
      await apiFetch(`/captcha-configs/${item.id}`, { method: "DELETE" });
      toast.success(`已删除 ${item.name}`);
      refresh();
      captchas.refetch();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "删除失败");
    } finally {
      setBusyCaptchaID(null);
    }
  }

  async function handleRefreshCaptchaBalance(item: CaptchaConfig) {
    setBusyCaptchaID(item.id);
    try {
      await apiFetch(`/captcha-configs/${item.id}/refresh-balance`, {
        method: "POST",
      });
      toast.success(`已更新 ${item.name} 剩余额度`);
      refresh();
      captchas.refetch();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "更新失败");
      refresh();
      captchas.refetch();
    } finally {
      setBusyCaptchaID(null);
    }
  }

  async function handleSave() {
    if (!form) return;
    const parts = externalChannelIDs.trim()
      ? externalChannelIDs.trim().split(/[,，\s]+/)
      : [];
    if (
      parts.some((part) =>
        !/^\d+$/.test(part) || !Number.isSafeInteger(Number(part)) || Number(part) <= 0,
      )
    ) {
      toast.error("外部 API 渠道 ID 必须是逗号分隔的正整数");
      return;
    }
    const token = externalAPIToken.trim();
    if (
      form.externalAPI.enabled &&
      ((token && token.length < 32) || (!token && !form.externalAPI.tokenConfigured))
    ) {
      toast.error("请为外部 API 配置至少 32 个字符的独立访问令牌");
      return;
    }
    setSaving(true);
    try {
      await apiFetch("/settings/config", {
        method: "PUT",
        body: JSON.stringify({
          ...form,
          externalAPI: {
            enabled: form.externalAPI.enabled,
            onlyCreatedKeyGroups: form.externalAPI.onlyCreatedKeyGroups,
            channelIDs: [...new Set(parts.map(Number))],
            token,
          },
        }),
      });
      toast.success("已写入配置文件");
      setConfigSavedPendingApply(true);
      query.refetch();
      refresh();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "保存失败");
    } finally {
      setSaving(false);
    }
  }

  async function handleApply() {
    setApplying(true);
    try {
      const result = await apiFetch<ApplyConfigResult>("/settings/apply", {
        method: "POST",
      });
      toast.success(result.message);
      setConfigSavedPendingApply(false);
      query.refetch();
      refresh();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "应用失败");
    } finally {
      setApplying(false);
    }
  }

  async function handleTestProxy() {
    setTestingProxy(true);
    try {
      const result = await apiFetch<ProxyTestResult>("/settings/proxy/test", {
        method: "POST",
        body: JSON.stringify(form?.proxy ?? {}),
      });
      if (result.ok) {
        toast.success(
          `代理可用，出口 IP ${result.ip}，延迟 ${result.latency_ms}ms`,
        );
      } else {
        toast.error(result.error ?? "代理测试失败");
      }
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "代理测试失败");
    } finally {
      setTestingProxy(false);
    }
  }

  async function handleCheckVersion() {
    setCheckingVersion(true);
    try {
      const result = await apiFetch<AppVersion>("/version?force=1");
      setVersionInfo(result);
      appVersion.setData(result);
      if (result.update_error) {
        toast.error(result.update_error);
      } else if (result.update_available && result.latest_version) {
        toast.warning(`发现新版本 ${result.latest_version}`);
      } else {
        toast.success("当前已是最新版本");
      }
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "检测更新失败");
    } finally {
      setCheckingVersion(false);
    }
  }

  return (
    <section className="space-y-4">
      <header className="space-y-2">
        <div className="flex flex-wrap items-center gap-2">
          <h1 className="text-lg font-semibold text-foreground">系统设置</h1>
          <Badge
            variant="outline"
            className="border-border bg-muted/40 text-muted-foreground"
          >
            动态配置中心
          </Badge>
        </div>
        <p className="max-w-3xl text-sm leading-6 text-muted-foreground">
          这里集中管理鉴权、调度、通知策略、通知渠道和验证码服务。保存只写入配置文件，应用会让鉴权、调度和通知策略立即生效；通知渠道和验证码服务本身是实时写库生效。
        </p>
        <p className="text-xs text-muted-foreground">
          配置文件路径：{query.data?.config_path ?? "—"}
        </p>
      </header>

      <Tabs value={activeTab} onValueChange={setActiveTab} className="space-y-4">
        <TabsList className="h-auto w-full justify-start rounded-2xl border border-border bg-muted/40 p-1">
          <TabsTrigger value="system" className="px-4 py-2">
            系统设置
          </TabsTrigger>
          <TabsTrigger value="notifications" className="px-4 py-2">
            通知渠道
          </TabsTrigger>
          <TabsTrigger value="captcha" className="px-4 py-2">
            验证码服务
          </TabsTrigger>
        </TabsList>

        <TabsContent value="system">
          <Card className="overflow-hidden border-border shadow-none">
            <CardContent className="space-y-8 p-4 sm:p-6">
              <SectionCard
                icon={<MonitorCog className="size-4 text-violet-600" />}
                title="应用信息"
                description="控制页面标题和通知标题前缀。"
              >
                <div className="mb-4 flex flex-wrap items-center gap-2 text-xs">
                  <Badge variant="outline" className="border-border bg-background">
                    当前版本 {versionInfo?.version || "加载中"}
                  </Badge>
                  {versionInfo?.latest_version ? (
                    <Badge
                      variant="outline"
                      className={cn(
                        "border-transparent",
                        versionInfo.update_available
                          ? "bg-amber-50 text-amber-700"
                          : "bg-emerald-50 text-emerald-700",
                      )}
                    >
                      {versionInfo.update_available
                        ? `可更新 ${versionInfo.latest_version}`
                        : "已是最新"}
                    </Badge>
                  ) : null}
                  <Button
                    size="sm"
                    variant="outline"
                    className="h-7 border-border bg-background px-2 text-xs"
                    onClick={handleCheckVersion}
                    disabled={checkingVersion}
                  >
                    <RefreshCw
                      className={cn(
                        "size-3.5",
                        checkingVersion ? "animate-spin" : "",
                      )}
                    />
                    {checkingVersion ? "检测中..." : "检测更新"}
                  </Button>
                </div>
                <div className="grid gap-4 md:grid-cols-2">
                  <Field
                    label="应用标题"
                    description="用于顶部标题和浏览器标签标题。"
                  >
                    <Input
                      value={form.app.title}
                      onChange={(e) =>
                        setForm((prev) =>
                          prev
                            ? {
                                ...prev,
                                app: { ...prev.app, title: e.target.value },
                              }
                            : prev,
                        )
                      }
                    />
                  </Field>
                  <Field
                    label="通知前缀"
                    description="为空时通知标题不添加前缀。"
                  >
                    <Input
                      value={form.app.notificationPrefix}
                      onChange={(e) =>
                        setForm((prev) =>
                          prev
                            ? {
                                ...prev,
                                app: {
                                  ...prev.app,
                                  notificationPrefix: e.target.value,
                                },
                              }
                            : prev,
                        )
                      }
                    />
                  </Field>
                </div>
              </SectionCard>

              <SectionCard
                icon={<KeyRound className="size-4 text-violet-600" />}
                title="外部分组倍率 API"
                description="向 NewAPI 下发本地持久化的分组倍率和 Key 指纹，普通请求不访问上游；保存并应用后生效。"
              >
                <InlineSwitch
                  id="external-api-enabled"
                  label="启用外部只读 API"
                  description="使用独立 Bearer 令牌读取快照；显式传入 force_refresh=1 时才即时回源刷新。"
                  checked={form.externalAPI.enabled}
                  onCheckedChange={(checked) =>
                    setForm((prev) =>
                      prev
                        ? { ...prev, externalAPI: { ...prev.externalAPI, enabled: checked } }
                        : prev,
                    )
                  }
                />
                <div className="mt-4">
                  <InlineSwitch
                    id="external-api-only-created-key-groups"
                    label="仅此接口：仅显示已创建密钥的分组"
                    description="独立控制外部 API；开启后分组列表只返回有密钥的分组，不改变渠道卡片、分组对话框或通知的筛选设置。"
                    checked={form.externalAPI.onlyCreatedKeyGroups}
                    onCheckedChange={(checked) =>
                      setForm((prev) =>
                        prev
                          ? { ...prev, externalAPI: { ...prev.externalAPI, onlyCreatedKeyGroups: checked } }
                          : prev,
                      )
                    }
                  />
                </div>
                <div className="mt-4 grid gap-4 md:grid-cols-2">
                  <Field
                    label="独立访问令牌"
                    description={form.externalAPI.tokenConfigured
                      ? "已配置，留空保留；输入新令牌可轮换。生成后请先复制并妥善保存。"
                      : "至少 32 个字符，生成后请先复制并妥善保存；保存后不再回显。"}
                  >
                    <Input
                      type="password"
                      autoComplete="new-password"
                      maxLength={4096}
                      value={externalAPIToken}
                      onChange={(e) => setExternalAPIToken(e.target.value)}
                      placeholder={form.externalAPI.tokenConfigured ? "留空保留现有令牌" : "输入或生成独立访问令牌"}
                    />
                    <div className="mt-2 flex gap-2">
                      <Button
                        type="button"
                        size="sm"
                        variant="outline"
                        onClick={() => {
                          const bytes = crypto.getRandomValues(new Uint8Array(32));
                          setExternalAPIToken(
                            Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join(""),
                          );
                        }}
                      >
                        生成令牌
                      </Button>
                      <Button
                        type="button"
                        size="sm"
                        variant="outline"
                        disabled={!externalAPIToken}
                        onClick={() => {
                          void copyText(externalAPIToken)
                            .then(() => toast.success("访问令牌已复制"))
                            .catch((err: Error) => toast.error(err.message));
                        }}
                      >
                        复制令牌
                      </Button>
                    </div>
                  </Field>
                  <Field
                    label="允许读取的渠道 ID"
                    description="逗号分隔，例如 1, 3；留空允许全部渠道。下方列出渠道名称与 ID。"
                  >
                    <Input
                      value={externalChannelIDs}
                      onChange={(e) => setExternalChannelIDs(e.target.value)}
                      placeholder="例如 1, 3"
                    />
                    <div className="max-h-24 overflow-y-auto text-xs leading-6 text-muted-foreground">
                      {channels.data?.map((channel) => (
                        <span key={channel.id} className="mr-3 inline-block">
                          {channel.name}（ID: {channel.id}）
                        </span>
                      ))}
                      {channels.error ? "渠道列表加载失败，可手动填写渠道 ID。" : null}
                    </div>
                  </Field>
                </div>
                <div className="mt-4">
                  <NoteBox title="NewAPI 本地匹配方式">
                    <span className="block break-all font-mono text-xs">
                      GET /api/external/v1/channel-rates
                    </span>
                    <span className="mt-2 block">
                      由 NewAPI 后端携带 Authorization: Bearer &lt;令牌&gt; 调用，令牌不能放入浏览器。
                      一次拉取允许渠道的分组、倍率和 api_keys 指纹，NewAPI 无需上传自己的渠道 Key。
                    </span>
                    <span className="mt-2 block">
                      普通请求只查本地数据库，缺少快照时也不会自动回源。
                      密钥分组与指纹存入独立的 channel_api_key_snapshots 表，不保存完整 Key。
                      启用后启动时补齐未同步数据，之后随“倍率扫描 Cron”后台更新允许的渠道。
                    </span>
                    <span className="mt-2 block break-all">
                      需要立即更新时，调用 GET /api/external/v1/channels/渠道ID/group-rates?force_refresh=1，
                      同时刷新该渠道的倍率和密钥快照。批量接口也支持此参数，建议优先按渠道强刷并预留请求超时。
                    </span>
                    <span className="mt-2 block break-all">
                      NewAPI 在本地按 key_fingerprint_normalization 归一化已有渠道 Key：
                      trim 表示去除首尾空白；trim_strip_sk_prefix 表示再去掉一个开头的 sk-。
                      对归一化结果的 UTF-8 字节计算 SHA-256 小写十六进制指纹，
                      结合 site_url 与 groups[].api_keys[].fingerprint 匹配所属分组，再读取 ratio。
                    </span>
                    <span className="mt-2 block">
                      分组仅遵循本节的独立筛选开关；渠道卡片和通知开关不会影响下发范围。
                      返回最近一次采集的倍率及 updated_at，密钥同步时间见 key_synced_at。
                      key_sync_status 为 ready、not_synced、partial 或 failed，分别表示已同步、尚未同步、部分可用或上次同步失败。
                      complete=false 或 key_fingerprints_complete=false 表示部分渠道或密钥暂不可用，
                      同步失败保留旧数据，应标记旧数据状态，避免将未匹配结果当成默认倍率。
                    </span>
                    <span className="mt-2 block">
                      仅下发指纹，不下发完整 Key。后台同步时，上游返回打码 Key 才在内部读取完整值计算摘要；
                      已有且未变更的指纹会复用，强制刷新时重新读取。不可用或未能更新的指纹通过 unavailable_api_key_ids 标记。
                    </span>
                    <span className="mt-2 block">
                      EXTERNAL_API_* 环境变量优先于本页配置，调整环境变量后需重新创建容器。
                    </span>
                  </NoteBox>
                </div>
              </SectionCard>

              <div className="grid grid-cols-1 gap-6 xl:grid-cols-[1.05fr_1fr]">
            <SectionCard
              icon={<ShieldCheck className="size-4 text-emerald-600" />}
              title="登录鉴权"
              description="控制后台是否需要登录，以及登录令牌的签发方式。"
            >
              <div className="grid gap-4 md:grid-cols-2">
                <InlineSwitch
                  id="auth-enabled"
                  label="启用登录鉴权"
                  description="关闭后前端将直接进入系统，不显示登录页。"
                  checked={form.auth.enabled}
                  onCheckedChange={(checked) =>
                    setForm((prev) =>
                      prev
                        ? { ...prev, auth: { ...prev.auth, enabled: checked } }
                        : prev,
                    )
                  }
                />
                <NoteBox title="热应用说明">
                  应用后新的鉴权配置立即生效，现有无效令牌会在后续请求时被拦截。
                </NoteBox>
              </div>
              <div className="mt-4 grid gap-4 md:grid-cols-2">
                <Field
                  label="管理员账号"
                  description="用于后台登录的固定账号。"
                >
                  <Input
                    value={form.auth.username}
                    onChange={(e) =>
                      setForm((prev) =>
                        prev
                          ? {
                              ...prev,
                              auth: { ...prev.auth, username: e.target.value },
                            }
                          : prev,
                      )
                    }
                  />
                </Field>
                <Field
                  label="登录有效期（小时）"
                  description="登录后令牌的有效时长。"
                >
                  <Input
                    type="number"
                    value={String(form.auth.sessionTTLHours)}
                    onChange={(e) =>
                      setForm((prev) =>
                        prev
                          ? {
                              ...prev,
                              auth: {
                                ...prev.auth,
                                sessionTTLHours: num(e.target.value),
                              },
                            }
                          : prev,
                      )
                    }
                  />
                </Field>
                <Field
                  label="管理员密码"
                  description="保存后写入配置文件，应用后用于新登录。"
                >
                  <Input
                    value={form.auth.password}
                    onChange={(e) =>
                      setForm((prev) =>
                        prev
                          ? {
                              ...prev,
                              auth: { ...prev.auth, password: e.target.value },
                            }
                          : prev,
                      )
                    }
                  />
                </Field>
                <Field
                  label="令牌签名密钥"
                  description="留空时回退使用安全主密钥。"
                >
                  <Input
                    value={form.auth.tokenSecret}
                    onChange={(e) =>
                      setForm((prev) =>
                        prev
                          ? {
                              ...prev,
                              auth: {
                                ...prev.auth,
                                tokenSecret: e.target.value,
                              },
                            }
                          : prev,
                      )
                    }
                  />
                </Field>
              </div>
            </SectionCard>

            <SectionCard
              icon={<Clock3 className="size-4 text-sky-600" />}
              title="调度与保留策略"
              description="管理余额采集、倍率采集和历史清理任务。"
            >
              <div className="grid gap-4 md:grid-cols-2">
                <Field
                  label="余额采集 Cron"
                  description="控制余额与消费同步的执行周期。"
                >
                  <Input
                    value={form.scheduler.balanceCron}
                    onChange={(e) =>
                      setForm((prev) =>
                        prev
                          ? {
                              ...prev,
                              scheduler: {
                                ...prev.scheduler,
                                balanceCron: e.target.value,
                              },
                            }
                          : prev,
                      )
                    }
                  />
                </Field>
                <Field
                  label="倍率采集 Cron"
                  description="控制分组倍率扫描的执行周期。"
                >
                  <Input
                    value={form.scheduler.rateCron}
                    onChange={(e) =>
                      setForm((prev) =>
                        prev
                          ? {
                              ...prev,
                              scheduler: {
                                ...prev.scheduler,
                                rateCron: e.target.value,
                              },
                            }
                          : prev,
                      )
                    }
                  />
                </Field>
                <Field
                  label="并发数"
                  description="调度器每轮最多同时处理的任务数。"
                >
                  <Input
                    type="number"
                    value={String(form.scheduler.concurrency)}
                    onChange={(e) =>
                      setForm((prev) =>
                        prev
                          ? {
                              ...prev,
                              scheduler: {
                                ...prev.scheduler,
                                concurrency: num(e.target.value),
                              },
                            }
                          : prev,
                      )
                    }
                  />
                </Field>
                <Field
                  label="清理任务 Cron"
                  description="留空则不执行历史数据清理。"
                >
                  <Input
                    value={form.scheduler.retention.cron}
                    onChange={(e) =>
                      setForm((prev) =>
                        prev
                          ? {
                              ...prev,
                              scheduler: {
                                ...prev.scheduler,
                                retention: {
                                  ...prev.scheduler.retention,
                                  cron: e.target.value,
                                },
                              },
                            }
                          : prev,
                      )
                    }
                  />
                </Field>
              </div>
              <div className="mt-4 grid gap-4 md:grid-cols-3">
                <Field
                  label="监控日志保留天数"
                  description="超过该天数的监控日志会被清理。"
                >
                  <Input
                    type="number"
                    value={String(form.scheduler.retention.monitorLogsDays)}
                    onChange={(e) =>
                      setForm((prev) =>
                        prev
                          ? {
                              ...prev,
                              scheduler: {
                                ...prev.scheduler,
                                retention: {
                                  ...prev.scheduler.retention,
                                  monitorLogsDays: num(e.target.value),
                                },
                              },
                            }
                          : prev,
                      )
                    }
                  />
                </Field>
                <Field
                  label="余额快照保留天数"
                  description="余额与消费趋势依赖这部分历史快照。"
                >
                  <Input
                    type="number"
                    value={String(
                      form.scheduler.retention.balanceSnapshotsDays,
                    )}
                    onChange={(e) =>
                      setForm((prev) =>
                        prev
                          ? {
                              ...prev,
                              scheduler: {
                                ...prev.scheduler,
                                retention: {
                                  ...prev.scheduler.retention,
                                  balanceSnapshotsDays: num(e.target.value),
                                },
                              },
                            }
                          : prev,
                      )
                    }
                  />
                </Field>
                <Field
                  label="通知日志保留天数"
                  description="通知发送结果的历史留存时长。"
                >
                  <Input
                    type="number"
                    value={String(
                      form.scheduler.retention.notificationLogsDays,
                    )}
                    onChange={(e) =>
                      setForm((prev) =>
                        prev
                          ? {
                              ...prev,
                              scheduler: {
                                ...prev.scheduler,
                                retention: {
                                  ...prev.scheduler.retention,
                                  notificationLogsDays: num(e.target.value),
                                },
                              },
                            }
                          : prev,
                      )
                    }
                  />
                </Field>
              </div>
            </SectionCard>
          </div>

          <SectionCard
            icon={<Bell className="size-4 text-amber-600" />}
            title="通知策略"
            description="这些项决定系统如何过滤和重试通知。"
          >
            <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
              <Field
                label="最小涨跌幅百分比"
                description="低于该值的倍率变化不发送通知。"
              >
                <Input
                  type="number"
                  step="0.01"
                  value={String(form.notifications.minChangePct)}
                  onChange={(e) =>
                    setForm((prev) =>
                      prev
                        ? {
                            ...prev,
                            notifications: {
                              ...prev.notifications,
                              minChangePct: Number(e.target.value || 0),
                            },
                          }
                        : prev,
                    )
                  }
                />
              </Field>
              <Field
                label="余额不足冷却分钟"
                description="同一渠道重复告警的抑制时间。"
              >
                <Input
                  type="number"
                  value={String(form.notifications.balanceLowCooldownMinutes)}
                  onChange={(e) =>
                    setForm((prev) =>
                      prev
                        ? {
                            ...prev,
                            notifications: {
                              ...prev.notifications,
                              balanceLowCooldownMinutes: num(e.target.value),
                            },
                          }
                        : prev,
                    )
                  }
                />
              </Field>
              <Field
                label="每日剩余提醒百分比"
                description="Sub2API 订阅每日剩余额度低于该百分比时提醒，0 为关闭。"
              >
                <Input
                  type="number"
                  step="0.1"
                  value={String(form.notifications.subscriptionDailyRemainingThresholdPct)}
                  onChange={(e) =>
                    setForm((prev) =>
                      prev
                        ? {
                            ...prev,
                            notifications: {
                              ...prev.notifications,
                              subscriptionDailyRemainingThresholdPct: Number(e.target.value || 0),
                            },
                          }
                        : prev,
                    )
                  }
                />
              </Field>
              <Field
                label="每周剩余提醒百分比"
                description="Sub2API 订阅每周剩余额度低于该百分比时提醒，0 为关闭。"
              >
                <Input
                  type="number"
                  step="0.1"
                  value={String(form.notifications.subscriptionWeeklyRemainingThresholdPct)}
                  onChange={(e) =>
                    setForm((prev) =>
                      prev
                        ? {
                            ...prev,
                            notifications: {
                              ...prev.notifications,
                              subscriptionWeeklyRemainingThresholdPct: Number(e.target.value || 0),
                            },
                          }
                        : prev,
                    )
                  }
                />
              </Field>
              <Field
                label="每月剩余提醒百分比"
                description="Sub2API 订阅每月剩余额度低于该百分比时提醒，0 为关闭。"
              >
                <Input
                  type="number"
                  step="0.1"
                  value={String(form.notifications.subscriptionMonthlyRemainingThresholdPct)}
                  onChange={(e) =>
                    setForm((prev) =>
                      prev
                        ? {
                            ...prev,
                            notifications: {
                              ...prev.notifications,
                              subscriptionMonthlyRemainingThresholdPct: Number(e.target.value || 0),
                            },
                          }
                        : prev,
                    )
                  }
                />
              </Field>
              <Field
                label="订阅到期提醒小时"
                description="Sub2API 订阅剩余小时数低于该值时提醒，0 为关闭。"
              >
                <Input
                  type="number"
                  value={String(form.notifications.subscriptionExpiryThresholdHours)}
                  onChange={(e) =>
                    setForm((prev) =>
                      prev
                        ? {
                            ...prev,
                            notifications: {
                              ...prev.notifications,
                              subscriptionExpiryThresholdHours: num(e.target.value),
                            },
                          }
                        : prev,
                    )
                  }
                />
              </Field>
              <Field
                label="订阅提醒冷却分钟"
                description="同一渠道同一类订阅提醒的冷却时间。"
              >
                <Input
                  type="number"
                  value={String(form.notifications.subscriptionAlertCooldownMinutes)}
                  onChange={(e) =>
                    setForm((prev) =>
                      prev
                        ? {
                            ...prev,
                            notifications: {
                              ...prev.notifications,
                              subscriptionAlertCooldownMinutes: num(e.target.value),
                            },
                          }
                        : prev,
                    )
                  }
                />
              </Field>
              <Field
                label="通知最大重试次数"
                description="发送失败后的最大尝试次数。"
              >
                <Input
                  type="number"
                  value={String(form.notifications.sendMaxAttempts)}
                  onChange={(e) =>
                    setForm((prev) =>
                      prev
                        ? {
                            ...prev,
                            notifications: {
                              ...prev.notifications,
                              sendMaxAttempts: num(e.target.value),
                            },
                          }
                        : prev,
                    )
                  }
                />
              </Field>
            </div>
          </SectionCard>

          <SectionCard
            icon={<Server className="size-4 text-indigo-600" />}
            title="上游请求"
            description="配置渠道访问上游站点时使用的超时时间和 User-Agent。"
          >
            <div className="grid gap-4 md:grid-cols-2">
              <Field label="超时时间（秒）" description="小于等于 0 时使用默认 30 秒。">
                <Input
                  type="number"
                  min={0}
                  value={String(form.upstream.timeoutSeconds)}
                  onChange={(e) =>
                    setForm((prev) =>
                      prev
                        ? {
                            ...prev,
                            upstream: {
                              ...prev.upstream,
                              timeoutSeconds: num(e.target.value),
                            },
                          }
                        : prev,
                    )
                  }
                />
              </Field>
              <Field label="User-Agent" description="为空时使用 upstream-ops/0.1。">
                <Input
                  value={form.upstream.userAgent}
                  placeholder="upstream-ops/0.1"
                  onChange={(e) =>
                    setForm((prev) =>
                      prev
                        ? {
                            ...prev,
                            upstream: {
                              ...prev.upstream,
                              userAgent: e.target.value,
                            },
                          }
                        : prev,
                    )
                  }
                />
              </Field>
            </div>
          </SectionCard>


          <SectionCard
            icon={<Network className="size-4 text-cyan-600" />}
            title="代理 IP"
            description="配置渠道上游请求使用的全局代理。只有渠道里开启代理 IP 的账号会使用这里的配置。"
            action={
              <Button
                size="sm"
                variant="outline"
                className="border-border bg-background"
                onClick={handleTestProxy}
                disabled={testingProxy}
              >
                <RefreshCw
                  className={cn("size-3.5", testingProxy ? "animate-spin" : "")}
                />
                {testingProxy ? "测试中..." : "测试代理"}
              </Button>
            }
          >
            <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
              <InlineSwitch
                id="proxy-enabled"
                label="启用全局代理"
                description="关闭后所有已勾选代理 IP 的对象也会保持直连。"
                checked={form.proxy.enabled}
                onCheckedChange={(checked) =>
                  setForm((prev) =>
                    prev
                      ? {
                          ...prev,
                          proxy: { ...prev.proxy, enabled: checked },
                        }
                      : prev,
                  )
                }
              />
              <InlineSwitch
                id="proxy-version-check"
                label="检测更新走代理"
                description="开启后顶部自动检测更新和这里的检测更新会使用代理。"
                checked={form.proxy.versionCheckEnabled}
                onCheckedChange={(checked) =>
                  setForm((prev) =>
                    prev
                      ? {
                          ...prev,
                          proxy: {
                            ...prev.proxy,
                            versionCheckEnabled: checked,
                          },
                        }
                      : prev,
                  )
                }
              />
              <Field label="协议" description="支持 HTTP、HTTPS 和 SOCKS5。">
                <Select
                  value={form.proxy.protocol}
                  onValueChange={(value) =>
                    setForm((prev) =>
                      prev
                        ? {
                            ...prev,
                            proxy: {
                              ...prev.proxy,
                              protocol: value as "http" | "https" | "socks5",
                            },
                          }
                        : prev,
                    )
                  }
                >
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="http">HTTP</SelectItem>
                    <SelectItem value="https">HTTPS</SelectItem>
                    <SelectItem value="socks5">SOCKS5</SelectItem>
                  </SelectContent>
                </Select>
              </Field>
              <Field label="主机" description="代理服务器地址，不含协议。">
                <Input
                  value={form.proxy.host}
                  placeholder="127.0.0.1"
                  onChange={(e) =>
                    setForm((prev) =>
                      prev
                        ? {
                            ...prev,
                            proxy: { ...prev.proxy, host: e.target.value },
                          }
                        : prev,
                    )
                  }
                />
              </Field>
              <Field label="端口" description="代理服务监听端口。">
                <Input
                  type="number"
                  value={String(form.proxy.port || "")}
                  placeholder="7890"
                  onChange={(e) =>
                    setForm((prev) =>
                      prev
                        ? {
                            ...prev,
                            proxy: { ...prev.proxy, port: num(e.target.value) },
                          }
                        : prev,
                    )
                  }
                />
              </Field>
              <Field label="账号（可选）" description="代理认证用户名。">
                <Input
                  value={form.proxy.username}
                  onChange={(e) =>
                    setForm((prev) =>
                      prev
                        ? {
                            ...prev,
                            proxy: { ...prev.proxy, username: e.target.value },
                          }
                        : prev,
                    )
                  }
                />
              </Field>
              <Field label="密码（可选）" description="代理认证密码。">
                <Input
                  type="password"
                  value={form.proxy.password}
                  onChange={(e) =>
                    setForm((prev) =>
                      prev
                        ? {
                            ...prev,
                            proxy: { ...prev.proxy, password: e.target.value },
                          }
                        : prev,
                    )
                  }
                />
              </Field>
            </div>
          </SectionCard>

          <div className="flex flex-wrap items-center gap-3 border-t border-border pt-5">
            <Button onClick={handleSave} disabled={saving || applying}>
              {saving ? "保存中..." : "保存"}
            </Button>
            <Button
              variant="outline"
              onClick={handleApply}
              disabled={saving || applying}
            >
              {applying ? "应用中..." : "应用"}
            </Button>
            <span
              className={cn(
                "text-xs",
                configSavedPendingApply
                  ? "font-medium text-amber-700"
                  : "text-muted-foreground",
              )}
            >
              {configSavedPendingApply
                ? "配置已保存但尚未应用，点击应用后才会立即生效。"
                : "保存写入配置文件，应用让鉴权、调度、通知策略、代理和上游请求配置立即更新。"}
            </span>
          </div>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="notifications">
          <SectionCard
            icon={<Send className="size-4 text-violet-600" />}
            title="通知渠道"
            description="管理 Telegram、Webhook、邮件、企业微信、钉钉、飞书等通知出口。"
            action={
              <Button
                size="sm"
                variant="outline"
                className="border-border bg-background"
                onClick={() => {
                  setEditingNotification(null);
                  setNotificationOpen(true);
                }}
              >
                <Plus className="size-3.5" />
                新增渠道
              </Button>
            }
          >
            <div className="mb-4 grid gap-3 md:grid-cols-3">
              <MiniMetric
                title="渠道总数"
                value={String(notifications.data?.length ?? 0)}
              />
              <MiniMetric
                title="最近发送"
                value={lastSent ? relativeTime(lastSent) : "—"}
              />
              <MiniMetric
                title="最近失败"
                value={String(recentFailed)}
                danger={recentFailed > 0}
              />
            </div>
            {notifications.loading ? (
              <EmptyLine text="通知渠道加载中..." />
            ) : !notifications.data || notifications.data.length === 0 ? (
              <EmptyPanel
                title="还没有通知渠道"
                description="新增一个通知渠道后，就可以用于余额告警、登录失败和倍率变化提醒。"
              />
            ) : (
              <div className="space-y-3">
                {notifications.data.map((channel) => {
                  const Icon = notifyIcon(channel.type);
                  const subCount = parseSubCount(channel.subscriptions);
                  return (
                    <div
                      key={channel.id}
                      className="rounded-2xl border border-border bg-background/80 p-4"
                    >
                      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                        <div className="flex min-w-0 items-start gap-3">
                          <div
                            className={cn(
                              "mt-0.5 flex size-9 shrink-0 items-center justify-center rounded-xl border",
                              channel.enabled
                                ? "border-violet-200 bg-violet-50 text-violet-700"
                                : "border-border bg-muted/40 text-muted-foreground",
                            )}
                          >
                            <Icon className="size-4" />
                          </div>
                          <div className="min-w-0 space-y-1">
                            <div className="flex flex-wrap items-center gap-2">
                              <p className="truncate text-sm font-semibold text-foreground">
                                {channel.name}
                              </p>
                              <Badge
                                variant="outline"
                                className="border-border bg-muted/40"
                              >
                                {typeLabel(channel.type)}
                              </Badge>
                              <Badge
                                variant="outline"
                                className={cn(
                                  "border-transparent",
                                  channel.enabled
                                    ? "bg-emerald-50 text-emerald-700"
                                    : "bg-slate-100 text-slate-500",
                                )}
                              >
                                {channel.enabled ? "启用中" : "已禁用"}
                              </Badge>
                              {channel.proxy_enabled ? (
                                <Badge
                                  variant="outline"
                                  className="border-transparent bg-cyan-50 text-cyan-700"
                                >
                                  代理 IP
                                </Badge>
                              ) : null}
                            </div>
                            <p className="text-xs text-muted-foreground">
                              {subCount === 0
                                ? "订阅全部渠道和分组"
                                : `已配置 ${subCount} 条订阅规则`}
                            </p>
                          </div>
                        </div>
                        <div className="flex flex-wrap items-center gap-2">
                          <Button
                            size="sm"
                            variant="outline"
                            disabled={busyNotificationID === channel.id}
                            onClick={() => handleTestNotification(channel)}
                          >
                            测试发送
                          </Button>
                          <Button
                            size="icon-sm"
                            variant="ghost"
                            onClick={() => {
                              setEditingNotification(channel);
                              setNotificationOpen(true);
                            }}
                          >
                            <PencilLine className="size-4" />
                          </Button>
                          <Button
                            size="icon-sm"
                            variant="ghost"
                            className="text-destructive hover:bg-destructive/10 hover:text-destructive"
                            disabled={busyNotificationID === channel.id}
                            onClick={() => handleDeleteNotification(channel)}
                          >
                            <Trash2 className="size-4" />
                          </Button>
                        </div>
                      </div>
                    </div>
                  );
                })}
              </div>
            )}
          </SectionCard>
        </TabsContent>

        <TabsContent value="captcha">
          <SectionCard
            icon={<KeyRound className="size-4 text-rose-600" />}
            title="验证码服务"
            description="管理用于处理 Turnstile 的打码平台，供渠道登录时自动调用。"
            action={
              <Button
                size="sm"
                variant="outline"
                className="border-border bg-background"
                onClick={() => {
                  setEditingCaptcha(null);
                  setCaptchaOpen(true);
                }}
              >
                <Plus className="size-3.5" />
                新增服务
              </Button>
            }
          >
            <div className="mb-4 grid gap-3 md:grid-cols-2">
              <MiniMetric
                title="服务数量"
                value={String(captchas.data?.length ?? 0)}
              />
              <MiniMetric
                title="用途"
                value="登录验证"
                hint="渠道启用 Turnstile 时调用"
              />
            </div>
            {captchas.loading ? (
              <EmptyLine text="验证码服务加载中..." />
            ) : !captchas.data || captchas.data.length === 0 ? (
              <EmptyPanel
                title="还没有验证码服务"
                description="如果某些渠道登录需要 Turnstile 验证，在这里接入 CapSolver、2Captcha 等服务。"
              />
            ) : (
              <div className="space-y-3">
                {captchas.data.map((item) => (
                  <div
                    key={item.id}
                    className="rounded-2xl border border-border bg-background/80 p-4"
                  >
                    <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                      <div className="space-y-1">
                        <div className="flex flex-wrap items-center gap-2">
                          <p className="text-sm font-semibold text-foreground">
                            {item.name}
                          </p>
                          <Badge
                            variant="outline"
                            className="border-border bg-muted/40"
                          >
                            {captchaLabel(item.type)}
                          </Badge>
                          <Badge
                            variant="outline"
                            className={cn(
                              "border-transparent",
                              item.enabled
                                ? "bg-emerald-50 text-emerald-700"
                                : "bg-slate-100 text-slate-500",
                            )}
                          >
                            {item.enabled ? "启用中" : "已禁用"}
                          </Badge>
                          {item.proxy_enabled ? (
                            <Badge
                              variant="outline"
                              className="border-transparent bg-cyan-50 text-cyan-700"
                            >
                              代理 IP
                            </Badge>
                          ) : null}
                        </div>
                        <p className="text-xs text-muted-foreground">
                          {item.endpoint
                            ? `自定义 Endpoint：${item.endpoint}`
                            : "使用平台默认 Endpoint"}
                        </p>
                        <p
                          className={cn(
                            "text-xs",
                            item.balance_error
                              ? "text-destructive"
                              : "text-muted-foreground",
                          )}
                        >
                          剩余额度：{formatCaptchaBalance(item)}
                          {item.balance_error
                            ? ` · ${item.balance_error}`
                            : item.balance_at
                              ? ` · 更新于 ${relativeTime(item.balance_at)}`
                              : " · 未更新"}
                        </p>
                      </div>
                      <div className="flex flex-wrap items-center gap-2">
                        <Button
                          size="icon-sm"
                          variant="ghost"
                          disabled={busyCaptchaID === item.id}
                          onClick={() => handleRefreshCaptchaBalance(item)}
                        >
                          <RefreshCw
                            className={cn(
                              "size-4",
                              busyCaptchaID === item.id ? "animate-spin" : "",
                            )}
                          />
                        </Button>
                        <Button
                          size="icon-sm"
                          variant="ghost"
                          onClick={() => {
                            setEditingCaptcha(item);
                            setCaptchaOpen(true);
                          }}
                        >
                          <PencilLine className="size-4" />
                        </Button>
                        <Button
                          size="icon-sm"
                          variant="ghost"
                          className="text-destructive hover:bg-destructive/10 hover:text-destructive"
                          disabled={busyCaptchaID === item.id}
                          onClick={() => handleDeleteCaptcha(item)}
                        >
                          <Trash2 className="size-4" />
                        </Button>
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </SectionCard>
        </TabsContent>

      </Tabs>

      <NotificationFormDialog
        open={notificationOpen}
        onOpenChange={(open) => {
          setNotificationOpen(open);
          if (!open) setEditingNotification(null);
        }}
        channel={editingNotification}
      />

      <CaptchaFormDialog
        open={captchaOpen}
        onOpenChange={(open) => {
          setCaptchaOpen(open);
          if (!open) setEditingCaptcha(null);
        }}
        config={editingCaptcha}
      />

      {confirmDialog}
    </section>
  );
}

function Field({
  label,
  description,
  children,
}: {
  label: string;
  description?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-2">
      <div className="space-y-1">
        <Label className="text-xs font-medium text-foreground">{label}</Label>
        {description ? (
          <p className="text-[11px] leading-5 text-muted-foreground">
            {description}
          </p>
        ) : null}
      </div>
      {children}
    </div>
  );
}

function SectionCard({
  icon,
  title,
  description,
  action,
  children,
}: {
  icon: React.ReactNode;
  title: string;
  description: string;
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <section className="rounded-3xl border border-border/80 bg-muted/20 p-5">
      <div className="mb-5 flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="space-y-1.5">
          <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
            {icon}
            {title}
          </div>
          <p className="max-w-2xl text-sm leading-6 text-muted-foreground">
            {description}
          </p>
        </div>
        {action}
      </div>
      {children}
    </section>
  );
}

function InlineSwitch({
  id,
  label,
  description,
  checked,
  onCheckedChange,
}: {
  id: string;
  label: string;
  description: string;
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
}) {
  return (
    <div className="flex items-start justify-between gap-4 rounded-2xl border border-border bg-background/90 px-4 py-3">
      <div className="space-y-1">
        <Label htmlFor={id} className="text-sm font-medium text-foreground">
          {label}
        </Label>
        <p className="text-[11px] leading-5 text-muted-foreground">
          {description}
        </p>
      </div>
      <Switch id={id} checked={checked} onCheckedChange={onCheckedChange} />
    </div>
  );
}

function NoteBox({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div className="rounded-2xl border border-emerald-200 bg-emerald-50/70 px-4 py-3 text-sm text-emerald-900">
      <p className="text-xs font-semibold uppercase tracking-[0.16em] text-emerald-700">
        {title}
      </p>
      <p className="mt-1 leading-6">{children}</p>
    </div>
  );
}

function MiniMetric({
  title,
  value,
  hint,
  danger = false,
}: {
  title: string;
  value: string;
  hint?: string;
  danger?: boolean;
}) {
  return (
    <div className="rounded-2xl border border-border bg-background/80 px-4 py-3">
      <p className="text-[11px] text-muted-foreground">{title}</p>
      <p
        className={cn(
          "mt-1 text-sm font-semibold",
          danger ? "text-destructive" : "text-foreground",
        )}
      >
        {value}
      </p>
      {hint ? (
        <p className="mt-1 text-[11px] text-muted-foreground">{hint}</p>
      ) : null}
    </div>
  );
}

function EmptyPanel({
  title,
  description,
}: {
  title: string;
  description: string;
}) {
  return (
    <div className="rounded-2xl border border-dashed border-border bg-background/70 px-4 py-6">
      <p className="text-sm font-medium text-foreground">{title}</p>
      <p className="mt-1 text-xs leading-5 text-muted-foreground">
        {description}
      </p>
    </div>
  );
}

function EmptyLine({ text }: { text: string }) {
  return <p className="text-sm text-muted-foreground">{text}</p>;
}

function typeLabel(type: NotificationChannelType) {
  const map: Record<NotificationChannelType, string> = {
    telegram: "Telegram",
    webhook: "Webhook",
    email: "邮件",
    wecom: "企业微信",
    dingtalk: "钉钉",
    feishu: "飞书",
    serverchan3: "Server酱³",
  };
  return map[type] ?? type;
}

function captchaLabel(type: CaptchaConfig["type"]) {
  const map: Record<CaptchaConfig["type"], string> = {
    capsolver: "CapSolver",
    "2captcha": "2Captcha",
    anticaptcha: "AntiCaptcha",
    yescaptcha: "YesCaptcha",
  };
  return map[type] ?? type;
}

function formatCaptchaBalance(item: CaptchaConfig) {
  if (item.last_balance == null) return "—";
  if (item.balance_unit === "points") return `${decimal(item.last_balance, 0)} 点`;
  return money(item.last_balance, { precise: true });
}

function notifyIcon(type: NotificationChannelType) {
  const map: Partial<Record<NotificationChannelType, typeof Send>> = {
    telegram: Send,
    webhook: Send,
    email: Send,
    wecom: Send,
    dingtalk: Send,
    feishu: Send,
    serverchan3: Send,
  };
  return map[type] ?? Send;
}

function parseSubCount(raw?: string) {
  if (!raw) return 0;
  try {
    const arr = JSON.parse(raw);
    return Array.isArray(arr) ? arr.length : 0;
  } catch {
    return 0;
  }
}
