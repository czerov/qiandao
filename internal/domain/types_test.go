package domain

import "testing"

func TestSignInProxyURL(t *testing.T) {
	tests := []struct {
		name     string
		settings Settings
		want     string
	}{
		{
			name: "global proxy wins",
			settings: Settings{
				ProxyURL: " http://global:7890 ",
				Providers: ProviderSettings{
					HDHive: HDHiveSettings{ProxyURL: "http://hdhive:7890"},
					JuYing: JuYingSettings{ProxyURL: "http://juying:7890"},
				},
			},
			want: "http://global:7890",
		},
		{
			name: "legacy hdhive proxy",
			settings: Settings{
				Providers: ProviderSettings{
					HDHive: HDHiveSettings{ProxyURL: "http://hdhive:7890"},
				},
			},
			want: "http://hdhive:7890",
		},
		{
			name: "legacy juying proxy",
			settings: Settings{
				Providers: ProviderSettings{
					JuYing: JuYingSettings{ProxyMode: "custom_proxy", ProxyURL: "http://juying:7890"},
				},
			},
			want: "http://juying:7890",
		},
		{
			name: "stale legacy juying direct proxy is ignored",
			settings: Settings{
				Providers: ProviderSettings{
					JuYing: JuYingSettings{ProxyMode: "direct", ProxyURL: "http://juying:7890"},
				},
			},
			want: "",
		},
		{
			name: "legacy juying telegram proxy mode",
			settings: Settings{
				Notify: NotifyConfig{TelegramProxyURL: "http://tg:7890"},
				Providers: ProviderSettings{
					JuYing: JuYingSettings{ProxyMode: "tg_proxy"},
				},
			},
			want: "http://tg:7890",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.settings.SignInProxyURL(); got != tt.want {
				t.Fatalf("SignInProxyURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
