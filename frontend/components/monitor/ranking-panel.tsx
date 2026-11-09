import { useState } from "react"
import { BarChart3, ListOrdered } from "lucide-react"
import {
  Bar,
  BarChart,
  CartesianGrid,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import type { DashboardRankingItem } from "@/lib/api-types"
import { money } from "@/lib/format"
import { cn } from "@/lib/utils"

/** 排行日期与采集时间始终使用后端声明的时区，不跟随浏览器所在地。 */
export function rankingTime(value: string | null | undefined, timeZone: string) {
  if (!value) return "时间未知"
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) return "时间未知"
  return new Intl.DateTimeFormat("zh-CN", {
    timeZone,
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hourCycle: "h23",
  }).format(date)
}

function RankingBadges({ item }: { item: DashboardRankingItem }) {
  return (
    <div className="mt-1 flex flex-wrap gap-1">
      <span className="text-[10px] text-muted-foreground">{item.type === "newapi" ? "NewAPI" : "Sub2API"}</span>
      {!item.monitor_enabled ? <Badge variant="secondary" className="px-1 py-0 text-[10px]">已暂停</Badge> : null}
      {item.last_error ? <Badge variant="outline" className="border-danger/30 px-1 py-0 text-[10px] text-danger" title={item.last_error}>采集异常</Badge> : null}
      {item.low_balance ? <Badge variant="outline" className="border-warning/30 px-1 py-0 text-[10px] text-warning">低余额</Badge> : null}
      {item.is_previous_day ? <Badge variant="secondary" className="px-1 py-0 text-[10px]">旧余额快照</Badge> : null}
      {!item.sampled_at ? <Badge variant="secondary" className="px-1 py-0 text-[10px]">时间未知</Badge> : null}
    </div>
  )
}

function RankingTooltip({ active, payload }: { active?: boolean; payload?: { payload?: DashboardRankingItem }[] }) {
  const item = payload?.[0]?.payload
  if (!active || !item) return null
  return (
    <div className="max-w-72 rounded-lg border border-border bg-popover px-3 py-2 shadow-md">
      <p className="break-words text-xs text-muted-foreground">{item.rank}. {item.name}</p>
      <p className="mt-1 text-sm font-semibold tabular-nums">{money(item.amount, { precise: true })}</p>
    </div>
  )
}

/** 中文与较宽字符按双倍宽度预留，避免长渠道名溢出固定宽度的分类轴。 */
function rankingChartLabel(label: string) {
  let units = 0
  let result = ""
  for (const character of label) {
    units += /[\u0020-\u007e]/.test(character) && !/[MW@#%]/.test(character) ? 1 : 2
    if (units > 16) return `${result}…`
    result += character
  }
  return result
}

function RankingChart({ items, kind }: { items: DashboardRankingItem[]; kind: "cost" | "balance" }) {
  const points = items.slice(0, 10)
  const values = points.map((item) => item.amount)
  const minimum = Math.min(0, ...values)
  const maximum = Math.max(0, ...values)
  const domain: [number, number] = [minimum < 0 ? minimum * 1.1 : 0, maximum > 0 ? maximum * 1.15 : minimum < 0 ? 0 : 1]
  const labels = new Map(points.map((item) => [item.channel_id, `${item.rank}. ${item.name}`]))

  return (
    <div>
      <div className="w-full" style={{ height: Math.max(180, points.length * 40 + 36) }}>
        <ResponsiveContainer width="100%" height="100%">
          <BarChart data={points} layout="vertical" margin={{ top: 6, right: 20, bottom: 4, left: 0 }} accessibilityLayer>
            <CartesianGrid stroke="var(--border)" strokeDasharray="3 3" horizontal={false} />
            <XAxis
              type="number"
              domain={domain}
              tickLine={false}
              axisLine={false}
              tick={{ fill: "var(--muted-foreground)", fontSize: 10 }}
              tickFormatter={(value: number) => money(value, { precise: value !== 0 && Math.abs(value) < 1 })}
              minTickGap={16}
            />
            <YAxis
              type="category"
              dataKey="channel_id"
              width={112}
              tickLine={false}
              axisLine={false}
              interval={0}
              tick={{ fill: "var(--muted-foreground)", fontSize: 11 }}
              tickFormatter={(value: number) => {
                const label = labels.get(value) ?? String(value)
                return rankingChartLabel(label)
              }}
            />
            <ReferenceLine x={0} stroke="var(--muted-foreground)" />
            <Tooltip content={<RankingTooltip />} cursor={{ fill: "var(--muted)" }} />
            <Bar dataKey="amount" fill={kind === "cost" ? "var(--warning)" : "var(--brand)"} maxBarSize={20} isAnimationActive={false} />
          </BarChart>
        </ResponsiveContainer>
      </div>
      <p className="mt-2 text-xs text-muted-foreground">图表最多展示当前排序的前 10 名；完整金额与采集状态请切换列表查看。</p>
    </div>
  )
}

interface RankingPanelProps {
  kind: "cost" | "balance"
  items: DashboardRankingItem[]
  validCount: number
  latestAt: string | null
  timeZone: string
  balanceOrder: "asc" | "desc"
}

export function RankingPanel({ kind, items, validCount, latestAt, timeZone, balanceOrder }: RankingPanelProps) {
  const [view, setView] = useState<"table" | "chart">("table")
  const isCost = kind === "cost"
  const visibleCount = view === "chart" ? Math.min(items.length, 10) : items.length

  return (
    <Card className="min-w-0 gap-4 border-border py-4 shadow-none">
      <CardHeader className="gap-3 px-4 sm:px-5">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <CardTitle className="text-base">{isCost ? "今日消耗排行榜" : "剩余余额排行榜"}</CardTitle>
            <p className="mt-1 text-xs text-muted-foreground">
              {isCost ? "当天有效累计消耗 · 从高到低" : balanceOrder === "asc" ? "最近已知余额 · 最低优先" : "最近已知余额 · 从高到低"}
            </p>
          </div>
          <div className="inline-flex shrink-0 rounded-md border border-border p-0.5" aria-label="榜单展示方式">
            <Button type="button" variant={view === "table" ? "secondary" : "ghost"} size="sm" className="h-7 gap-1 px-2 text-xs" aria-pressed={view === "table"} onClick={() => setView("table")}>
              <ListOrdered className="size-3.5" />列表
            </Button>
            <Button type="button" variant={view === "chart" ? "secondary" : "ghost"} size="sm" className="h-7 gap-1 px-2 text-xs" aria-pressed={view === "chart"} onClick={() => setView("chart")}>
              <BarChart3 className="size-3.5" />图表
            </Button>
          </div>
        </div>
        <p className="text-xs text-muted-foreground">已显示 {visibleCount} / {validCount} 个有效渠道 · 最近采集 {rankingTime(latestAt, timeZone)}</p>
      </CardHeader>
      <CardContent className="min-w-0 px-4 sm:px-5">
        {items.length === 0 ? (
          <div className="flex min-h-48 items-center justify-center rounded-lg border border-dashed border-border px-4 text-center text-sm text-muted-foreground">
            {isCost ? "当前范围暂无当天有效消耗，等待采集后参与排名。" : "当前范围暂无已知余额，等待首次采集。"}
          </div>
        ) : view === "chart" ? (
          <RankingChart items={items} kind={kind} />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-12">排名</TableHead>
                <TableHead>渠道</TableHead>
                <TableHead className="text-right">{isCost ? "今日消耗" : "剩余余额"}</TableHead>
                {isCost ? <TableHead className="text-right">占比</TableHead> : null}
                <TableHead className="text-right">采集时间</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((item) => (
                <TableRow key={item.channel_id}>
                  <TableCell className={cn("tabular-nums font-medium", item.rank <= 3 && "text-brand")}>{item.rank}</TableCell>
                  <TableCell className="max-w-64 whitespace-normal">
                    <span className="block max-w-64 truncate font-medium" title={item.name}>{item.name}</span>
                    <RankingBadges item={item} />
                    {item.tags.length > 0 ? <p className="mt-1 max-w-64 truncate text-[10px] text-muted-foreground" title={item.tags.join(" · ")}>{item.tags.join(" · ")}</p> : null}
                  </TableCell>
                  <TableCell className={cn("text-right tabular-nums font-medium", item.low_balance && "text-warning")}>{money(item.amount, { precise: true })}</TableCell>
                  {isCost ? <TableCell className="text-right tabular-nums text-muted-foreground">{item.share == null ? "—" : `${(item.share * 100).toFixed(1)}%`}</TableCell> : null}
                  <TableCell className="text-right text-xs tabular-nums text-muted-foreground">{rankingTime(item.sampled_at, timeZone)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  )
}
