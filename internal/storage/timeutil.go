package storage

import "time"

var resetTZ = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err == nil {
		return loc
	}
	return time.FixedZone("CST", 8*60*60)
}()

func ymdInResetTZ(ms int64) int {
	t := time.UnixMilli(ms).In(resetTZ)
	year, month, day := t.Date()
	return year*10000 + int(month)*100 + day
}

func dayBoundsMsInResetTZ(ms int64) (startMs int64, endMs int64) {
	t := time.UnixMilli(ms).In(resetTZ)
	year, month, day := t.Date()
	start := time.Date(year, month, day, 0, 0, 0, 0, resetTZ)
	end := start.Add(24 * time.Hour)
	return start.UnixMilli(), end.UnixMilli()
}
