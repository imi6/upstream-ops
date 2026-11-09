import { useEffect, useState } from "react"
import { AlertCircle, CalendarDays, RefreshCw, Search, Trophy, Users, Wallet } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { RankingPanel, rankingTime } from "@/components/monitor/ranking-panel"
import type { ChannelType, DashboardUnrankedChannel, RankingCostStatus } from "@/lib/api-types"
import { money } from "@/lib/format"
import { useDashboardRankings } from "@/lib/queries"

const costStatusLabels: Record<RankingCostStatus, string> = {
  ready: "已计入今日消耗",
  missing: "尚无消耗数据",
  unknown_time: "消费采集时间未知",
  not_synced_today: "今日尚未同步",
  invalid_time: "消费采集时间异常",
}

function UnrankedChannels({ channels, timeZone }: { channels: DashboardUnrankedChannel[]; timeZone: string }) {
  if (channels.length === 0) return null
  return (
    <details className="rounded-xl border border-border bg-card p-4 sm:p-5">
      <summary className="cursor-pointer text-sm font-medium">未参与部分榜单的渠道（{channels.length}）</summary>
      <p className="mt-2 text-xs leading-5 text-muted-foreground">缺失与过期消费不按 0 计入；渠道可能仍有有效余额并参与余额榜。真实采集到的 0 消耗正常参与排名。</p>
      <div className="mt-3">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>渠道</TableHead>
              <TableHead>今日消耗</TableHead>
              <TableHead>消费采集时间</TableHead>
              <TableHead>余额数据</TableHead>
              <TableHead>余额采集时间</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {channels.map((channel) => (
              <TableRow key={channel.channel_id}>
                <TableCell className="max-w-64 whitespace-normal">
                  <span className="block max-w-64 truncate font-medium" title={channel.name}>{channel.name}</span>
                  <div className="mt-1 flex flex-wrap gap-1">
                    <span className="text-[10px] text-muted-foreground">{channel.type === "newapi" ? "NewAPI" : "Sub2API"}</span>
                    {!channel.monitor_enabled ? <Badge variant="secondary" className="px-1 py-0 text-[10px]">已暂停</Badge> : null}
                    {channel.last_error ? <Badge variant="outline" className="border-danger/30 px-1 py-0 text-[10px] text-danger" title={channel.last_error}>采集异常</Badge> : null}
                  </div>
                </TableCell>
                <TableCell className="text-xs">{costStatusLabels[channel.cost_status]}</TableCell>
                <TableCell className="text-xs tabular-nums text-muted-foreground">{rankingTime(channel.today_cost_at, timeZone)}</TableCell>
                <TableCell className="text-xs">{channel.balance_status === "ready" ? "已计入余额榜" : "尚无有效余额"}</TableCell>
                <TableCell className="text-xs tabular-nums text-muted-foreground">{rankingTime(channel.last_balance_at, timeZone)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </details>
  )
}

export default function RankingsPage() {
  const [search, setSearch] = useState("")
  const [queryText, setQueryText] = useState("")
  const [type, setType] = useState<ChannelType | "all">("all")
  const [tag, setTag] = useState("")
  const [includePaused, setIncludePaused] = useState(true)
  const [limit, setLimit] = useState<"10" | "20" | "all">("10")
  const [balanceOrder, setBalanceOrder] = useState<"asc" | "desc">("desc")

  useEffect(() => {
    const id = setTimeout(() => setQueryText(search.trim()), 250)
    return () => clearTimeout(id)
  }, [search])

  const rankings = useDashboardRankings({
    q: queryText,
    type: type === "all" ? undefined : type,
    tag,
    include_paused: includePaused,
    limit,
    balance_order: balanceOrder,
  })
  const changingFilters = rankings.stale || search.trim() !== queryText
  // 条件切换时不展示旧条件下的合计与排名，普通后台轮询仍保留原数据。
  const data = changingFilters ? null : rankings.data
  const summary = data?.summary
  const availableTags = rankings.data?.available_tags ?? []

  return (
    <section className="space-y-4">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-lg font-semibold"><Trophy className="size-5 text-brand" />渠道排行榜</h1>
          <p className="mt-1 text-xs leading-5 text-muted-foreground">
            {data ? `统计日期 ${data.date} · ${data.time_zone} · 按渠道账号排行` : "按渠道账号展示今日消耗与最近已知余额"}
          </p>
        </div>
        <Button type="button" variant="outline" size="sm" className="gap-1.5" onClick={rankings.refetch} disabled={rankings.loading}>
          <RefreshCw className="size-3.5" />刷新列表
        </Button>
      </header>

      <Card className="gap-3 border-border p-4 shadow-none">
        <div className="flex flex-wrap items-end gap-3">
          <label className="min-w-48 flex-1 text-xs text-muted-foreground">
            搜索渠道
            <div className="relative mt-1.5">
              <Search className="pointer-events-none absolute left-2.5 top-2.5 size-4" />
              <Input className="h-9 pl-8" placeholder="输入渠道名称" value={search} maxLength={200} onChange={(event) => setSearch(event.target.value)} />
            </div>
          </label>
          <label className="text-xs text-muted-foreground">上游类型
            <Select value={type} onValueChange={(value) => setType(value as ChannelType | "all")}>
              <SelectTrigger className="mt-1.5 w-32" aria-label="上游类型"><SelectValue /></SelectTrigger>
              <SelectContent><SelectItem value="all">全部类型</SelectItem><SelectItem value="newapi">NewAPI</SelectItem><SelectItem value="sub2api">Sub2API</SelectItem></SelectContent>
            </Select>
          </label>
          <label className="text-xs text-muted-foreground">渠道标签
            <Select value={tag ? `tag:${tag}` : "all"} onValueChange={(value) => setTag(value === "all" ? "" : value.slice(4))}>
              <SelectTrigger className="mt-1.5 w-36" aria-label="渠道标签"><SelectValue placeholder="全部标签" /></SelectTrigger>
              <SelectContent><SelectItem value="all">全部标签</SelectItem>{availableTags.map((value) => <SelectItem key={value} value={`tag:${value}`}>{value}</SelectItem>)}</SelectContent>
            </Select>
          </label>
          <label className="text-xs text-muted-foreground">展示数量
            <Select value={limit} onValueChange={(value) => setLimit(value as "10" | "20" | "all")}>
              <SelectTrigger className="mt-1.5 w-28" aria-label="展示数量"><SelectValue /></SelectTrigger>
              <SelectContent><SelectItem value="10">Top 10</SelectItem><SelectItem value="20">Top 20</SelectItem><SelectItem value="all">全部渠道</SelectItem></SelectContent>
            </Select>
          </label>
          <label className="text-xs text-muted-foreground">余额排序
            <Select value={balanceOrder} onValueChange={(value) => setBalanceOrder(value as "asc" | "desc")}>
              <SelectTrigger className="mt-1.5 w-36" aria-label="余额排序"><SelectValue /></SelectTrigger>
              <SelectContent><SelectItem value="desc">余额从高到低</SelectItem><SelectItem value="asc">余额最低优先</SelectItem></SelectContent>
            </Select>
          </label>
          <label className="flex h-9 cursor-pointer items-center gap-2 text-xs">
            <Checkbox checked={includePaused} onCheckedChange={(checked) => setIncludePaused(checked === true)} />包含暂停渠道
          </label>
        </div>
        <p className="text-xs leading-5 text-muted-foreground">合计与占比基于当前筛选范围的全部有效数据，不受 Top 数量限制。列表每 30 秒读取本地快照，后台标签页暂停轮询；刷新列表不会触发上游采集。</p>
        <p className="text-xs leading-5 text-muted-foreground">日期按北京时间划分；Sub2API 的今日金额沿用上游站点统计口径。</p>
      </Card>

      {rankings.error ? (
        <div role="alert" className="flex items-start gap-2 rounded-lg border border-danger/30 bg-danger/5 p-3 text-sm text-danger">
          <AlertCircle className="mt-0.5 size-4 shrink-0" />
          <div><p>{data ? "刷新失败，当前保留上次读取的数据。" : "排行榜读取失败，请刷新列表重试。"}</p><p className="mt-1 break-all text-xs">{rankings.error}</p></div>
        </div>
      ) : null}

      {data && summary ? (
        <>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
            <Card className="gap-2 border-border p-4 shadow-none">
              <p className="flex items-center gap-1.5 text-xs text-muted-foreground"><Wallet className="size-3.5" />今日有效消耗合计</p>
              <p className="text-2xl font-semibold tabular-nums" title={money(summary.today_cost_channels > 0 ? summary.today_cost_total : null, { precise: true })}>{money(summary.today_cost_channels > 0 ? summary.today_cost_total : null)}</p>
              <p className="text-xs text-muted-foreground">{summary.today_cost_channels} 个渠道有当天有效数据</p>
            </Card>
            <Card className="gap-2 border-border p-4 shadow-none">
              <p className="flex items-center gap-1.5 text-xs text-muted-foreground"><Wallet className="size-3.5" />当前已知余额合计</p>
              <p className="text-2xl font-semibold tabular-nums" title={money(summary.balance_channels > 0 ? summary.balance_total : null, { precise: true })}>{money(summary.balance_channels > 0 ? summary.balance_total : null)}</p>
              <p className="text-xs text-muted-foreground">{summary.balance_channels} 个渠道有余额快照，包含旧快照</p>
            </Card>
            <Card className="gap-2 border-border p-4 shadow-none">
              <p className="flex items-center gap-1.5 text-xs text-muted-foreground"><Users className="size-3.5" />当前范围渠道数</p>
              <p className="text-2xl font-semibold tabular-nums">{summary.filtered_channels}<span className="text-sm font-normal text-muted-foreground"> / {summary.total_channels}</span></p>
              <p className="text-xs text-muted-foreground">{includePaused ? "包含暂停渠道" : "仅包含启用监控的渠道"}</p>
            </Card>
            <Card className="gap-2 border-border p-4 shadow-none">
              <p className="flex items-center gap-1.5 text-xs text-muted-foreground"><CalendarDays className="size-3.5" />今日消耗未同步</p>
              <p className="text-2xl font-semibold tabular-nums text-warning">{summary.today_cost_pending_channels}</p>
              <p className="text-xs text-muted-foreground">另有 {summary.balance_missing_channels} 个渠道暂无有效余额</p>
            </Card>
          </div>
          <p className="text-xs text-muted-foreground">列表读取时间 {rankingTime(data.generated_at, data.time_zone)}；各渠道金额以独立采集时间为准，使用采集时已完成充值倍率换算的结果。</p>
          {summary.filtered_channels === 0 ? (
            <div className="rounded-xl border border-dashed border-border px-4 py-12 text-center text-sm text-muted-foreground">当前筛选没有匹配渠道，请调整搜索或筛选条件。</div>
          ) : (
            <div className="grid min-w-0 grid-cols-1 items-start gap-4 xl:grid-cols-2">
              <RankingPanel kind="cost" items={data.cost_ranking} validCount={summary.today_cost_channels} latestAt={summary.latest_cost_at} timeZone={data.time_zone} balanceOrder={balanceOrder} />
              <RankingPanel kind="balance" items={data.balance_ranking} validCount={summary.balance_channels} latestAt={summary.latest_balance_at} timeZone={data.time_zone} balanceOrder={balanceOrder} />
            </div>
          )}
          <UnrankedChannels channels={data.unranked_channels} timeZone={data.time_zone} />
        </>
      ) : !rankings.error ? (
        <div role="status" className="rounded-xl border border-dashed border-border px-4 py-14 text-center text-sm text-muted-foreground">{changingFilters ? "正在加载当前筛选条件下的榜单…" : "正在读取排行榜快照…"}</div>
      ) : null}
    </section>
  )
}
