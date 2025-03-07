package featureprobe

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

var VERSION string = "1.1.0"
var USER_AGENT string = "Go/" + VERSION

type FeatureProbe struct {
	Config   FPConfig
	Repo     *Repository
	Syncer   *Synchronizer
	Recorder *EventRecorder
}

type FPConfig struct {
	RemoteUrl       string
	TogglesUrl      string
	EventsUrl       string
	ServerSdkKey    string
	RefreshInterval int
	WaitFirstResp   bool
}

type FPBoolDetail struct {
	Value     bool
	RuleIndex *int
	Version   *uint64
	Reason    string
}

type FPNumberDetail struct {
	Value     float64
	RuleIndex *int
	Version   *uint64
	Reason    string
}

type FPStrDetail struct {
	Value     string
	RuleIndex *int
	Version   *uint64
	Reason    string
}

type FPJsonDetail struct {
	Value     interface{}
	RuleIndex *int
	Version   *uint64
	Reason    string
}

type Option func(fpConfig *FPConfig)

func WithTogglesUri(uri string) Option {
	return func(fpConfig *FPConfig) {
		fpConfig.TogglesUrl = fpConfig.RemoteUrl + uri
	}
}

func WithEventsUri(uri string) Option {
	return func(fpConfig *FPConfig) {
		fpConfig.EventsUrl = fpConfig.RemoteUrl + uri
	}
}

func WithRefreshInterval(interval int) Option {
	return func(fpConfig *FPConfig) {
		fpConfig.RefreshInterval = interval
	}
}

func WithWaitFirstResp(wait bool) Option {
	return func(fpConfig *FPConfig) {
		fpConfig.WaitFirstResp = wait
	}
}

func NewTestClient(opts ...Option) (FeatureProbe, error) {
	return NewFeatureProbe("", "", opts...)
}

func NewFeatureProbe(remoteUrl, severSdkKey string, opts ...Option) (FeatureProbe, error) {
	repo := Repository{}
	if !strings.HasSuffix(remoteUrl, "/") {
		remoteUrl += "/"
	}
	fpConfig := FPConfig{
		RemoteUrl:       remoteUrl,
		TogglesUrl:      remoteUrl + "api/server-sdk/toggles",
		EventsUrl:       remoteUrl + "api/events",
		ServerSdkKey:    severSdkKey,
		RefreshInterval: 2000,
		WaitFirstResp:   true,
	}

	for _, opt := range opts {
		opt(&fpConfig)
	}

	timeout := time.Duration(fpConfig.RefreshInterval)
	eventRecorder := NewEventRecorder(fpConfig.EventsUrl, timeout, fpConfig.ServerSdkKey)
	eventRecorder.Start()

	toggleSyncer := NewSynchronizer(fpConfig.TogglesUrl, timeout, fpConfig.ServerSdkKey, &repo)
	toggleSyncer.Start(fpConfig.WaitFirstResp)

	return FeatureProbe{
		Config:   fpConfig,
		Repo:     &repo,
		Syncer:   &toggleSyncer,
		Recorder: &eventRecorder,
	}, nil
}

func newToggleForTest(key string, value interface{}) Toggle {
	s := 0
	return Toggle{
		Key:           key,
		Enabled:       true,
		DefaultServe:  Serve{Select: &s},
		DisabledServe: Serve{Select: &s},
		Version:       0,
		ForClient:     false,
		Variations:    []interface{}{value},
		Rules:         []Rule{},
	}
}

func NewFeatureProbeForTest(toggles map[string]interface{}) FeatureProbe {
	repo := Repository{}
	repo.Toggles = map[string]Toggle{}
	for key, value := range toggles {
		repo.Toggles[key] = newToggleForTest(key, value)
	}
	return FeatureProbe{
		Repo: &repo,
	}
}

func (fp *FeatureProbe) BoolValue(toggle string, user FPUser, defaultValue bool) bool {
	val, _, _, _ := fp.genericDetail(toggle, user, defaultValue)
	r, ok := val.(bool)
	if !ok {
		return defaultValue
	}
	return r
}

func (fp *FeatureProbe) StrValue(toggle string, user FPUser, defaultValue string) string {
	val, _, _, _ := fp.genericDetail(toggle, user, defaultValue)
	r, ok := val.(string)
	if !ok {
		return defaultValue
	}
	return r
}

func (fp *FeatureProbe) NumberValue(toggle string, user FPUser, defaultValue float64) float64 {
	val, _, _, _ := fp.genericDetail(toggle, user, defaultValue)
	i, ok := val.(int)
	if ok {
		return float64(i)
	}
	f, ok := val.(float64)
	if !ok {
		return defaultValue
	}
	return f
}

func (fp *FeatureProbe) JsonValue(toggle string, user FPUser, defaultValue interface{}) interface{} {
	val, _, _, _ := fp.genericDetail(toggle, user, defaultValue)
	return val
}

func (fp *FeatureProbe) genericDetail(toggle string, user FPUser, defaultValue interface{}) (interface{}, *int, *uint64, string) {
	value := defaultValue
	reason := fmt.Sprintf("Toggle:[%s] not exist", toggle)
	var ruleIndex *int = nil
	var version *uint64 = nil
	var variationIndex *int = nil

	if fp.Repo == nil {
		return value, ruleIndex, version, reason
	}
	t, ok := fp.Repo.Toggles[toggle]
	if !ok {
		return value, ruleIndex, version, reason
	}
	detail, err := t.evalDetail(user, fp.Repo.Segments)

	variationIndex = detail.VariationIndex
	ruleIndex = detail.RuleIndex
	version = detail.Version
	reason = detail.Reason

	if err == nil {
		value = detail.Value
	}

	if fp.Recorder != nil {
		fp.Recorder.RecordAccess(AccessEvent{
			Time:    time.Now().UnixNano() / 1e6,
			Key:     toggle,
			Value:   value,
			Index:   variationIndex,
			Version: version,
			Reason:  reason,
		})
	}

	return value, ruleIndex, version, reason
}

func (fp *FeatureProbe) BoolDetail(toggle string, user FPUser, defaultValue bool) FPBoolDetail {
	value, ruleIndex, version, reason := fp.genericDetail(toggle, user, defaultValue)
	detail := FPBoolDetail{Value: defaultValue, RuleIndex: ruleIndex, Version: version, Reason: reason}

	val, ok := value.(bool)
	if !ok {
		detail.Reason = "Value type mismatch"
		return detail
	}
	detail.Value = val
	return detail
}

func (fp *FeatureProbe) StrDetail(toggle string, user FPUser, defaultValue string) FPStrDetail {
	value, ruleIndex, version, reason := fp.genericDetail(toggle, user, defaultValue)
	detail := FPStrDetail{Value: defaultValue, RuleIndex: ruleIndex, Version: version, Reason: reason}

	val, ok := value.(string)
	if !ok {
		detail.Reason = "Value type mismatch"
		return detail
	}
	detail.Value = val
	return detail
}

func (fp *FeatureProbe) NumberDetail(toggle string, user FPUser, defaultValue float64) FPNumberDetail {
	value, ruleIndex, version, reason := fp.genericDetail(toggle, user, defaultValue)
	detail := FPNumberDetail{Value: defaultValue, RuleIndex: ruleIndex, Version: version, Reason: reason}

	val, ok := value.(float64)
	if !ok {
		detail.Reason = "Value type mismatch"
		return detail
	}
	detail.Value = val
	return detail
}

func (fp *FeatureProbe) JsonDetail(toggle string, user FPUser, defaultValue interface{}) FPJsonDetail {
	value, ruleIndex, version, reason := fp.genericDetail(toggle, user, defaultValue)
	detail := FPJsonDetail{Value: value, RuleIndex: ruleIndex, Version: version, Reason: reason}
	return detail
}

func (fp *FeatureProbe) setRepoForTest(repo Repository) {
	fp.Repo = &repo
}

func newHttpClient(timeout time.Duration) http.Client {
	return http.Client{
		Timeout: timeout * time.Millisecond,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 10 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   2 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

func (fp *FeatureProbe) Close() {
	if fp.Syncer != nil {
		fp.Syncer.Stop()
	}
	if fp.Repo != nil {
		fp.Repo.Clear()
	}
	if fp.Recorder != nil {
		fp.Recorder.Stop()
	}
}
