package api

import (
	"net/http"
	"sync"
	"time"

	"strings"

	"io/ioutil"

	"strconv"

	"fmt"

	"github.com/Jeffail/gabs"
	"github.com/pkg/errors"
	"github.com/airking05/go-exchange-chart-fetcher/models"
)

const (
	HITBTC_BASE_URL = "https://api.hitbtc.com/api/2"
)

func NewHitbtcApiUsingConfigFunc(f func(*HitbtcApiConfig)) (ExchangeApi, error) {
	conf := &HitbtcApiConfig{
		BaseURL:           HITBTC_BASE_URL,
		RateCacheDuration: 30 * time.Second,
	}
	f(conf)

	api := &HitbtcApi{
		rateMap:         nil,
		volumeMap:       nil,
		rateLastUpdated: time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),

		m: new(sync.Mutex),
		c: conf,
	}
	api.fetchSettlements()
	return api, nil
}

type HitbtcApiConfig struct {
	ExchangeId models.ExchangeID
	Apikey     string
	ApiSecret  string
	BaseURL    string

	RateCacheDuration time.Duration
}

type HitbtcApi struct {
	volumeMap       map[string]map[string]float64
	rateMap         map[string]map[string]float64
	rateLastUpdated time.Time

	settlements []string

	m *sync.Mutex
	c *HitbtcApiConfig
}

func (h *HitbtcApi) GetExchangeId() models.ExchangeID {
	return h.c.ExchangeId
}

func (h *HitbtcApi) publicApiUrl(command string) string {
	return h.c.BaseURL + "/public/" + command
}

func (h *HitbtcApi) fetchSettlements() error {
	settlements := make([]string, 0)
	url := h.publicApiUrl("symbol")
	resp, err := http.Get(url)
	if err != nil {
		return errors.Wrapf(err, "failed to fetch %s", url)
	}
	defer resp.Body.Close()

	byteArray, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return errors.Wrapf(err, "failed to fetch %s", url)
	}
	json, err := gabs.ParseJSON(byteArray)

	if err != nil {
		return errors.Wrapf(err, "failed to parse json")
	}

	pairMap, err := json.Children()
	if err != nil {
		return errors.Wrapf(err, "failed to parse json")
	}
	for _, v := range pairMap {
		settlement, ok := v.Path("quoteCurrency").Data().(string)
		if !ok {
			continue
		}
		settlements = append(settlements, settlement)
	}
	m := make(map[string]bool)
	uniq := []string{}
	for _, ele := range settlements {
		if !m[ele] {
			m[ele] = true
			uniq = append(uniq, ele)
		}
	}
	h.settlements = uniq
	fmt.Println(uniq)
	return nil
}

func (h *HitbtcApi) fetchRate() error {
	h.rateMap = make(map[string]map[string]float64)
	h.volumeMap = make(map[string]map[string]float64)
	url := h.publicApiUrl("ticker")
	resp, err := http.Get(url)
	if err != nil {
		return errors.Wrapf(err, "failed to fetch %s", url)
	}
	defer resp.Body.Close()

	byteArray, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return errors.Wrapf(err, "failed to fetch %s", url)
	}
	json, err := gabs.ParseJSON(byteArray)

	if err != nil {
		return errors.Wrapf(err, "failed to parse json")
	}

	rateMap, err := json.Children()
	if err != nil {
		return errors.Wrapf(err, "failed to parse json")
	}
	for _, v := range rateMap {
		pair, ok := v.Path("symbol").Data().(string)
		if !ok {
			continue
		}

		var settlement string
		var trading string
		for _, s := range h.settlements {
			index := strings.LastIndex(pair, s)
			if index != 0 && index == len(pair)-len(s) {
				settlement = s
				trading = pair[0:index]
			}
		}
		if settlement == "" || trading == "" {
			continue

		}
		// update rate
		last, ok := v.Path("last").Data().(string)
		if !ok {
			continue
		}

		lastf, err := strconv.ParseFloat(last, 64)
		if err != nil {
			return err
		}

		m, ok := h.rateMap[trading]
		if !ok {
			m = make(map[string]float64)
			h.rateMap[trading] = m
		}
		m[settlement] = lastf

		// update volume
		volume, ok := v.Path("volume").Data().(string)
		if !ok {
			continue
		}
		volumef, err := strconv.ParseFloat(volume, 64)
		if err != nil {
			return err
		}

		m, ok = h.volumeMap[trading]
		if !ok {
			m = make(map[string]float64)
			h.volumeMap[trading] = m
		}
		m[settlement] = volumef
	}

	return nil
}

func (h *HitbtcApi) CurrencyPairs() ([]*CurrencyPair, error) {
	h.m.Lock()
	defer h.m.Unlock()

	now := time.Now()
	if now.Sub(h.rateLastUpdated) >= h.c.RateCacheDuration {
		err := h.fetchRate()
		if err != nil {
			return nil, err
		}
		h.rateLastUpdated = now
	}

	var pairs []*CurrencyPair
	for trading, m := range h.rateMap {
		for settlement := range m {
			pair := &CurrencyPair{
				Trading:    trading,
				Settlement: settlement,
			}
			pairs = append(pairs, pair)
		}
	}

	return pairs, nil
}

func (h *HitbtcApi) Volume(trading string, settlement string) (float64, error) {
	h.m.Lock()
	defer h.m.Unlock()

	now := time.Now()
	if now.Sub(h.rateLastUpdated) >= h.c.RateCacheDuration {
		err := h.fetchRate()
		if err != nil {
			return 0, err
		}
		h.rateLastUpdated = now
	}
	if m, ok := h.volumeMap[trading]; !ok {
		return 0, errors.Errorf("%s/%s", trading, settlement)
	} else if volume, ok := m[settlement]; !ok {
		return 0, errors.Errorf("%s/%s", trading, settlement)
	} else {
		return volume, nil
	}
}

func (h *HitbtcApi) Rate(trading string, settlement string) (float64, error) {
	h.m.Lock()
	defer h.m.Unlock()

	if trading == settlement {
		return 1, nil
	}

	now := time.Now()
	if now.Sub(h.rateLastUpdated) >= h.c.RateCacheDuration {
		err := h.fetchRate()
		if err != nil {
			return 0, err
		}
		h.rateLastUpdated = now
	}
	if m, ok := h.rateMap[trading]; !ok {
		return 0, errors.Errorf("%s/%s", trading, settlement)
	} else if rate, ok := m[settlement]; !ok {
		return 0, errors.Errorf("%s/%s", trading, settlement)
	} else {
		return rate, nil
	}
}
