package connector

import "time"

// CostTimeZone 是消费采集、今日排行与消费趋势共同使用的自然日时区。
const CostTimeZone = "Asia/Shanghai"

var costLocation = func() *time.Location {
	location, err := time.LoadLocation(CostTimeZone)
	if err != nil {
		return time.FixedZone(CostTimeZone, 8*60*60)
	}
	return location
}()

// CostDayStart 返回指定时刻对应的北京时间零点，不依赖服务进程的 TZ。
func CostDayStart(at time.Time) time.Time {
	local := at.In(costLocation)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, costLocation)
}
