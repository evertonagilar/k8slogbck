# 📦 k8slogbck – Arquivador de Logs de Pods Kubernetes

`k8slogbck` é uma ferramenta leve desenvolvida em Go para monitorar eventos de finalização de Pods em clusters Kubernetes e arquivar seus logs automaticamente em um diretório de backup. O sistema também realiza uma varredura periódica para garantir que nenhum log antigo fique para trás.

## 🚀 Propósito

O objetivo principal é garantir que os logs gerados por pods Kubernetes, especialmente os que são finalizados ou excluídos, sejam copiados de forma segura para um local permanente, mesmo em ambientes com alta rotatividade de workloads, como clusters de desenvolvimento, CI/CD ou ambientes efêmeros.

## ⚙️ Como funciona

- **Watcher de pods:** Escuta eventos de `UPDATE` e `DELETE` no cluster Kubernetes.
- **Arquivamento sob demanda:** Ao detectar que um pod está sendo encerrado, o sistema localiza os logs no diretório padrão do kubelet (`/var/log/pods`) e copia para a pasta de backup configurada (`/backup`).
- **Varredura periódica:** A cada 1 minuto, arquivos `.gz` antigos com mais de 60 segundos e não copiados anteriormente também são arquivados.

## 📁 Estrutura esperada dos logs

O kubelet geralmente organiza os logs assim:

```
/var/log/pods/{namespace}_{pod}_{uid}/{container}/0.log
```

## 💾 Backup

Os logs são copiados para:

```
/backup/{namespace}/{pod}/{timestamp}-{filename}
```

Exemplo:

```
/backup/dev-api/my-app-0/20250711-154233-0.log
```

## 🛠️ Configuração

Você pode configurar a ferramenta por meio de variáveis de ambiente:

| Variável             | Descrição                                                                 |
|----------------------|---------------------------------------------------------------------------|
| `BACKUP_PATTERN`     | Lista de namespaces separados por vírgula para monitoramento (ex: `p-*,prod-*`) |
| `REMOVE_AFTER_COPY`  | Se definido como `true`, os arquivos originais são apagados após cópia    |

### Exemplo de configuração:

```bash
BACKUP_PATTERN="dev-,prod-" REMOVE_AFTER_COPY=true ./k8slogbck
```

## 📦 Requisitos

- Rodar como container no cluster Kubernetes
- Ter acesso a:
  - `/var/log/pods` (geralmente montado no host via `hostPath`)
  - Um volume persistente ou pasta local para `/backup`

## 🐳 Exemplo de Deployment no Kubernetes

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: k8slogbck
spec:
  replicas: 1
  selector:
    matchLabels:
      app: k8slogbck
  template:
    metadata:
      labels:
        app: k8slogbck
    spec:
      containers:
        - name: k8slogbck
          image: evertonagilar/k8slogbck:1.0.0
          env:
            - name: BACKUP_PATTERN
              value: "p-*,prod-*"         # qualquer namespace com esses prefixos
            - name: REMOVE_AFTER_COPY
              value: "true"
        volumeMounts:
          - name: log-path
        mountPath: /var/log/pods
          - name: backup-dest
        mountPath: /backup
      volumes:
        - name: log-path
          hostPath:
          path: /var/log/pods
          type: Directory
        - name: backup-dest
          hostPath:
          path: /var/log/k8s-log-backup
          type: DirectoryOrCreate
```

## ✅ Status atual

- ✅ Watcher eficiente via Informer
- ✅ Cópia segura de arquivos `.log`, `.log.*` e `.gz`
- ✅ Arquivamento periódico automático

## 🧪 Possíveis melhorias futuras

- [ ] Verificação de uso de disco (para evitar cópias com disco cheio)
- [ ] Compressão opcional de logs não `.gz`
- [ ] Webhook para integração com sistemas de alerta
- [ ] Painel de controle (UI)

## 📄 Licença

MIT

---

Feito para DevOps e SREs que precisam preservar logs mesmo após a vida curta dos pods.