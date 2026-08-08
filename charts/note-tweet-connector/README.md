# note-tweet-connector

![Version: 2.2.0](https://img.shields.io/badge/Version-2.2.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 3.2.0](https://img.shields.io/badge/AppVersion-3.2.0-informational?style=flat-square)

A Helm chart for the Note Tweet Connector

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` |  |
| args.idleTimeout | string | `"60s"` |  |
| args.logLevel | string | `"info"` |  |
| args.metricsPort | int | `9090` |  |
| args.port | int | `8080` |  |
| args.readTimeout | string | `"15s"` |  |
| args.shutdownTimeout | string | `"30s"` |  |
| args.trackerDbPath | string | `"/app/data/tracker.sqlite"` |  |
| args.trackerRetention | string | `"2160h"` |  |
| args.twitterMediaHosts | string | `"pbs.twimg.com,video.twimg.com"` |  |
| args.twitterOAuth2RedirectURL | string | `"https://example.tld/twitter/callback"` |  |
| args.twitterStreamKeepAliveTimeout | string | `"90s"` |  |
| args.twitterTokenStorePath | string | `"/app/data/twitter_oauth2_token.json"` |  |
| args.twitterUsername | string | `""` |  |
| args.writeTimeout | string | `"15s"` |  |
| discord.enabled | bool | `false` |  |
| discord.errorDedupeWindow | string | `"10m"` |  |
| discord.notifyTimeout | string | `"5s"` |  |
| discord.streamLoopThreshold | int | `5` |  |
| discord.streamLoopWindow | string | `"10m"` |  |
| env | list | `[]` |  |
| fullnameOverride | string | `""` |  |
| image.pullPolicy | string | `"IfNotPresent"` |  |
| image.repository | string | `"ghcr.io/soli0222/note-tweet-connector"` |  |
| image.tag | string | `""` |  |
| imagePullSecrets | list | `[]` |  |
| ingress.annotations | object | `{}` |  |
| ingress.className | string | `""` |  |
| ingress.enabled | bool | `false` |  |
| ingress.hosts[0].host | string | `"example.tld"` |  |
| ingress.hosts[0].paths[0].path | string | `"/"` |  |
| ingress.hosts[0].paths[0].pathType | string | `"ImplementationSpecific"` |  |
| ingress.tls | list | `[]` |  |
| livenessProbe.failureThreshold | int | `3` |  |
| livenessProbe.httpGet.path | string | `"/healthz"` |  |
| livenessProbe.httpGet.port | string | `"http"` |  |
| livenessProbe.initialDelaySeconds | int | `10` |  |
| livenessProbe.periodSeconds | int | `10` |  |
| livenessProbe.timeoutSeconds | int | `5` |  |
| metricsService.enabled | bool | `true` |  |
| metricsService.port | int | `9090` |  |
| metricsService.type | string | `"ClusterIP"` |  |
| monitoring.prometheusRule.additionalRules | list | `[]` |  |
| monitoring.prometheusRule.annotations | object | `{}` |  |
| monitoring.prometheusRule.enabled | bool | `false` |  |
| monitoring.prometheusRule.labels | object | `{}` |  |
| monitoring.prometheusRule.namespace | string | `""` |  |
| monitoring.prometheusRule.rules.errorRate.enabled | bool | `true` |  |
| monitoring.prometheusRule.rules.errorRate.for | string | `"5m"` |  |
| monitoring.prometheusRule.rules.errorRate.severity | string | `"warning"` |  |
| monitoring.prometheusRule.rules.errorRate.threshold | int | `5` |  |
| monitoring.prometheusRule.rules.serviceDown.enabled | bool | `true` |  |
| monitoring.prometheusRule.rules.serviceDown.for | string | `"5m"` |  |
| monitoring.prometheusRule.rules.serviceDown.severity | string | `"critical"` |  |
| monitoring.prometheusRule.rules.slowProcessing.enabled | bool | `true` |  |
| monitoring.prometheusRule.rules.slowProcessing.for | string | `"5m"` |  |
| monitoring.prometheusRule.rules.slowProcessing.severity | string | `"warning"` |  |
| monitoring.prometheusRule.rules.slowProcessing.threshold | int | `10` |  |
| monitoring.serviceMonitor.annotations | object | `{}` |  |
| monitoring.serviceMonitor.enabled | bool | `false` |  |
| monitoring.serviceMonitor.interval | string | `"30s"` |  |
| monitoring.serviceMonitor.jobLabel | string | `""` |  |
| monitoring.serviceMonitor.labels | object | `{}` |  |
| monitoring.serviceMonitor.metricRelabelings | list | `[]` |  |
| monitoring.serviceMonitor.namespace | string | `""` |  |
| monitoring.serviceMonitor.path | string | `"/metrics"` |  |
| monitoring.serviceMonitor.relabelings | list | `[]` |  |
| monitoring.serviceMonitor.scrapeTimeout | string | `"10s"` |  |
| monitoring.serviceMonitor.selector | object | `{}` |  |
| nameOverride | string | `""` |  |
| nodeSelector | object | `{}` |  |
| persistence.accessModes[0] | string | `"ReadWriteOnce"` |  |
| persistence.annotations | object | `{}` |  |
| persistence.enabled | bool | `true` |  |
| persistence.existingClaim | string | `""` |  |
| persistence.mountPath | string | `"/app/data"` |  |
| persistence.size | string | `"1Gi"` |  |
| persistence.storageClass | string | `""` |  |
| podAnnotations | object | `{}` |  |
| podLabels | object | `{}` |  |
| podSecurityContext.fsGroup | int | `10001` |  |
| readinessProbe.failureThreshold | int | `3` |  |
| readinessProbe.httpGet.path | string | `"/healthz"` |  |
| readinessProbe.httpGet.port | string | `"http"` |  |
| readinessProbe.initialDelaySeconds | int | `5` |  |
| readinessProbe.periodSeconds | int | `5` |  |
| readinessProbe.timeoutSeconds | int | `3` |  |
| replicaCount | int | `1` |  |
| resources | object | `{}` |  |
| secrets.env[0].key | string | `"MISSKEY_HOOK_SECRET"` |  |
| secrets.env[0].name | string | `"MISSKEY_HOOK_SECRET"` |  |
| secrets.env[1].key | string | `"MISSKEY_HOST"` |  |
| secrets.env[1].name | string | `"MISSKEY_HOST"` |  |
| secrets.env[2].key | string | `"MISSKEY_TOKEN"` |  |
| secrets.env[2].name | string | `"MISSKEY_TOKEN"` |  |
| secrets.env[3].key | string | `"MISSKEY_MEDIA_HOST"` |  |
| secrets.env[3].name | string | `"MISSKEY_MEDIA_HOST"` |  |
| secrets.env[4].key | string | `"TWITTER_OAUTH2_CLIENT_ID"` |  |
| secrets.env[4].name | string | `"TWITTER_OAUTH2_CLIENT_ID"` |  |
| secrets.env[5].key | string | `"TWITTER_BEARER_TOKEN"` |  |
| secrets.env[5].name | string | `"TWITTER_BEARER_TOKEN"` |  |
| secrets.env[6].enabledWhenDiscord | bool | `true` |  |
| secrets.env[6].key | string | `"DISCORD_WEBHOOK_URL"` |  |
| secrets.env[6].name | string | `"DISCORD_WEBHOOK_URL"` |  |
| secrets.secretName | string | `""` |  |
| securityContext.runAsNonRoot | bool | `true` |  |
| securityContext.runAsUser | int | `10001` |  |
| service.port | int | `8080` |  |
| service.type | string | `"ClusterIP"` |  |
| serviceAccount.annotations | object | `{}` |  |
| serviceAccount.create | bool | `true` |  |
| serviceAccount.name | string | `""` |  |
| tolerations | list | `[]` |  |

----------------------------------------------
Autogenerated from chart metadata using [helm-docs v1.14.2](https://github.com/norwoodj/helm-docs/releases/v1.14.2)
