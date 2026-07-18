package normalize

import (
	"fmt"
	"strings"
	"time"

	"github.com/gocrane/crane/pkg/cost/model"
	"github.com/gocrane/crane/pkg/cost/provider/internal/jsonx"
)

func Decimal(object jsonx.Object, names ...string) (model.Decimal, error) {
	value := object.String(names...)
	if value == "" {
		return model.Zero, nil
	}
	return model.NewDecimal(value)
}

func Currency(value string) model.Currency {
	value = strings.ToUpper(strings.TrimSpace(value))
	switch value {
	case "", "RMB", "YUAN", "￥", "¥":
		return model.CurrencyCNY
	default:
		return model.Currency(value)
	}
}

func Time(object jsonx.Object, names ...string) (time.Time, error) {
	value := strings.TrimSpace(object.String(names...))
	if value == "" {
		return time.Time{}, nil
	}
	formats := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006-01-02",
		"20060102150405",
	}
	for _, format := range formats {
		if parsed, err := time.ParseInLocation(format, value, time.Local); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported cloud billing time %q", value)
}

func ChargeModel(value string) model.ChargeModel {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "payasyougo", "pay-as-you-go", "pay_as_you_go", "postpaid", "post_pay", "2", "按量计费", "按量付费":
		return model.ChargeModelPayAsYouGo
	case "subscription", "prepaid", "pre_pay", "1", "包年包月", "包月":
		return model.ChargeModelSubscription
	case "spot", "spotpaid", "竞价实例", "抢占式实例":
		return model.ChargeModelSpot
	case "contract", "3", "合同计费":
		return model.ChargeModelContract
	default:
		return model.ChargeModelUnknown
	}
}

func Category(service, product string) model.CostCategory {
	value := strings.ToLower(service + " " + product)
	switch {
	case containsAny(value, "ecs", "cvm", "elastic cloud server", "cloud server", "云服务器", "bare metal", "bms", "gpu"):
		return model.CostCategoryCompute
	case containsAny(value, "disk", "ebs", "cbs", "evs", "storage", "云硬盘", "快照", "snapshot"):
		return model.CostCategoryStorage
	case containsAny(value, "bandwidth", "eip", "load balancer", "elb", "clb", "nat", "cdn", "带宽", "负载均衡"):
		return model.CostCategoryNetwork
	case containsAny(value, "kubernetes", "ack", "tke", "vke", "cce", "容器服务", "control plane"):
		return model.CostCategoryControlPlane
	case containsAny(value, "monitor", "logging", "prometheus", "日志", "监控"):
		return model.CostCategoryObservability
	case containsAny(value, "rds", "redis", "database", "数据库"):
		return model.CostCategoryDatabase
	default:
		return model.CostCategoryOther
	}
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
