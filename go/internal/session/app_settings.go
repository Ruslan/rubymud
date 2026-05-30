package session

import "strings"

const (
	settingAllowExecCommand     = "allow_exec_command"
	settingAllowWebFetchCommand = "allow_webfetch_command"
)

func settingBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (s *Session) appSettingEnabled(key string) bool {
	if s == nil || s.store == nil {
		return false
	}
	value, err := s.store.GetSetting(key)
	if err != nil {
		return false
	}
	return settingBool(value)
}

func (s *Session) allowExecCommand() bool {
	return s.appSettingEnabled(settingAllowExecCommand)
}

func (s *Session) allowWebFetchCommand() bool {
	return s.appSettingEnabled(settingAllowWebFetchCommand)
}
