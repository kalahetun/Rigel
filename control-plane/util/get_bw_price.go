package util

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// 全局模型变量
var bandwidthModel *BandwidthCostModel

const (
	bandwidthPricing = "bandwidth_pricing.json"
)

type BandwidthCostModel struct {
	Unit       string                        `json:"unit"`
	Dimension  string                        `json:"dimension"`
	Continents []string                      `json:"continents"`
	Providers  map[string]map[string]float64 `json:"providers"`
}

// LoadBandwidthCost 初始化全局变量 bandwidthModel，同时打日志
func LoadBandwidthCost(logger *slog.Logger) error {
	exePath, err := os.Executable()
	if err != nil {
		logger.Error("获取程序路径失败", "err", err)
		return fmt.Errorf("获取程序路径失败: %w", err)
	}

	exeDir := filepath.Dir(exePath)
	costFile := filepath.Join(exeDir, bandwidthPricing)
	logger.Info("加载带宽价格文件", "file", costFile)

	data, err := os.ReadFile(costFile)
	if err != nil {
		logger.Error("读取 bandwidth cost 文件失败", "err", err)
		return fmt.Errorf("读取 bandwidth cost 文件失败: %w", err)
	}

	var model BandwidthCostModel
	if err := json.Unmarshal(data, &model); err != nil {
		logger.Error("解析 bandwidth cost JSON 失败", "err", err)
		return fmt.Errorf("解析 bandwidth cost JSON 失败: %w", err)
	}

	bandwidthModel = &model
	b, _ := json.Marshal(model)
	logger.Info("带宽价格模型加载成功", "providers", b)

	return nil
}

// GetBandwidthPrice 使用全局变量 bandwidthModel，同时打日志
func GetBandwidthPrice(provider, srcContinent, dstContinent string, logger *slog.Logger) (float64, error) {
	if bandwidthModel == nil {
		logger.Info("bandwidthModel 未初始化，开始加载")
		if err := LoadBandwidthCost(logger); err != nil {
			return 0, err
		}
	}

	if providerMap, ok := bandwidthModel.Providers[provider]; ok {
		var price float64
		var found bool

		switch provider {
		case "gcp":
			key := fmt.Sprintf("%s->%s", srcContinent, dstContinent)
			price, found = providerMap[key]
			if !found {
				key = fmt.Sprintf("Any->%s", dstContinent)
				price, found = providerMap[key]
				if !found {
					price, found = providerMap["Any->Any"]
				}
			}
		case "aws", "digitalocean":
			price, found = providerMap["Any->Any"]
		case "azure":
			if srcContinent == dstContinent {
				price, found = providerMap["SameContinent"]
			} else {
				price, found = providerMap["DifferentContinent"]
			}
		case "vultr":
			key := fmt.Sprintf("%s->Any", srcContinent)
			price, found = providerMap[key]
			if !found {
				price, found = providerMap["Any->Any"]
			}
		default:
			logger.Error("不支持的 provider", "provider", provider)
			return 0, fmt.Errorf("不支持的 provider: %s", provider)
		}

		if !found {
			logger.Error("未找到匹配价格", "provider", provider, "src", srcContinent, "dst", dstContinent)
			//给一个高价
			return 0.2, fmt.Errorf("未找到匹配价格: %s %s->%s", provider, srcContinent, dstContinent)
		}

		logger.Info("获取带宽价格", "provider", provider, "src", srcContinent, "dst", dstContinent, "price", price)
		return price, nil
	}

	logger.Error("provider 不存在", "provider", provider)
	return 0.2, fmt.Errorf("provider 不存在: %s", provider)
}
