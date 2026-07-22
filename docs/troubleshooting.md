# Troubleshooting

Commands and information to troubleshoot `backup-restore-operator`

## Show logs

```
kubectl  -n cattle-resources-system logs -l app.kubernetes.io/name=rancher-backup --tail=-1
```

## List local restore archives

```
kubectl -n cattle-resources-system exec deploy/rancher-backup   -- ls /var/lib/backups
```

## List events in `cattle-resources-system ` namespace

```
kubectl -n cattle-resources-system  get events
```
