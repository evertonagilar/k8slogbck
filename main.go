package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

var (
	logBase       = "/var/log/pods"
	backupPath    = "/backup"
	patternList   = []string{}
	removeAfterCp = false
)

const version = "1.0.0"

func main() {
	printHeader()

	if env := os.Getenv("BACKUP_PATTERN"); env != "" {
		patternList = strings.Split(env, ",")
		logInfo("🧭 BACKUP_PATTERN definido: %v", patternList)
	} else {
		logWarn("🔄 BACKUP_PATTERN não definido. Usando '*' para todos os namespaces.")
		patternList = []string{"*"}
	}

	if env := os.Getenv("REMOVE_AFTER_COPY"); strings.ToLower(env) == "true" {
		removeAfterCp = true
		logInfo("🗑️ Modo de remoção pós-cópia ATIVADO.")
	} else {
		logInfo("📁 Modo de remoção pós-cópia DESATIVADO.")
	}

	logInfo("🚀 Iniciando monitoramento de pods e arquivamento de logs...")

	config, err := rest.InClusterConfig()
	if err != nil {
		logFatal("Erro ao obter config in-cluster: %v", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		logFatal("Erro ao criar clientset: %v", err)
	}

	go periodicArchive()

	stopCh := make(chan struct{})
	defer close(stopCh)

	startPodInformer(clientset, stopCh)

	<-stopCh // bloqueia main
}

func startPodInformer(clientset *kubernetes.Clientset, stopCh <-chan struct{}) {
	factory := informers.NewSharedInformerFactory(clientset, 0)
	informer := factory.Core().V1().Pods().Informer()

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(oldObj, newObj interface{}) {
			pod, ok := newObj.(*v1.Pod)
			if !ok {
				logWarn("❓ Objeto não é Pod.")
				return
			}

			if pod.DeletionTimestamp != nil {
				logInfo("📦 Pod em finalização: %s/%s | Phase: %s", pod.Namespace, pod.Name, pod.Status.Phase)
				if shouldArchive(pod.Namespace) {
					go archivePodLogsInformer(pod.Namespace, pod.Name)
				}
			}
		},
		DeleteFunc: func(obj interface{}) {
			pod, ok := obj.(*v1.Pod)
			if !ok {
				logWarn("❓ Objeto não é um Pod.")
				return
			}
			logInfo("📦 Pod excluído: %s/%s", pod.Namespace, pod.Name)
			if shouldArchive(pod.Namespace) {
				go archivePodLogsInformer(pod.Namespace, pod.Name)
			}
		},
	})

	logInfo("📡 Iniciando informer de pods...")
	go informer.Run(stopCh)

	if !cache.WaitForCacheSync(stopCh, informer.HasSynced) {
		logFatal("❌ Falha ao sincronizar cache do informer.")
	}
}

func periodicArchive() {
	for {
		logInfo("⏱️ Iniciando varredura periódica de logs antigos (*.gz)...")
		for _, pattern := range patternList {
			globPath := filepath.Join(logBase, pattern+"_*")
			matches, err := filepath.Glob(globPath)
			if err != nil {
				logError("Erro ao fazer globbing: %v", err)
				continue
			}
			for _, podDir := range matches {
				err := filepath.Walk(podDir, func(path string, info os.FileInfo, err error) error {
					if err != nil || info.IsDir() {
						return nil
					}
					if strings.HasSuffix(path, ".gz") && fileOlderThan(path, 60) && isReadableAndNotEmpty(path) {
						ns, pod := extractNamespaceAndPod(path)
						destDir := filepath.Join(backupPath, ns, pod)
						if err := os.MkdirAll(destDir, 0755); err != nil {
							logWarn("⚠️ Não foi possível criar diretório %s: %v", destDir, err)
							return nil
						}
						suffix := time.Now().Format("20060102-150405")
						dst := filepath.Join(destDir, fmt.Sprintf("%s-%s", suffix, filepath.Base(path)))
						if fileExists(dst) {
							logWarn("⚠️ Arquivo já existente, ignorado: %s", dst)
							return nil
						}
						logInfo("📦 Arquivando log: %s → %s", path, dst)
						copyFile(path, dst)
						if removeAfterCp {
							os.Remove(path)
							logInfo("🗑️ Removido original: %s", path)
						}
					}
					return nil
				})
				if err != nil {
					logError("Erro ao caminhar por %s: %v", podDir, err)
				}
			}
		}
		time.Sleep(1 * time.Minute)
	}
}

func archivePodLogsInformer(namespace, podName string) {
	globPattern := filepath.Join(logBase, fmt.Sprintf("%s_%s_*", namespace, podName))
	matches, err := filepath.Glob(globPattern)
	if err != nil || len(matches) == 0 {
		logWarn("📁 Nenhum diretório encontrado para pod %s/%s", namespace, podName)
		return
	}

	for _, podDir := range matches {
		logInfo("📁 Verificando diretório: %s", podDir)
		err := filepath.Walk(podDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			logInfo("Arquivo detectado: %s", path)
			if strings.HasSuffix(path, ".gz") || strings.HasSuffix(path, ".log") || strings.Contains(path, ".log.") {
				destDir := filepath.Join(backupPath, namespace, podName)
				if err := os.MkdirAll(destDir, 0755); err != nil {
					logWarn("⚠️ Não foi possível criar diretório %s: %v", destDir, err)
					return nil
				}
				suffix := time.Now().Format("20060102-150405")
				dst := filepath.Join(destDir, fmt.Sprintf("%s-%s", suffix, filepath.Base(path)))
				if fileExists(dst) {
					logWarn("⚠️ Arquivo já existente, ignorado: %s", dst)
					return nil
				}
				logInfo("📦 Copiando %s → %s", path, dst)
				copyFile(path, dst)
				if removeAfterCp {
					os.Remove(path)
					logInfo("🗑️ Removido original: %s", path)
				}
			} else {
				logInfo("Arquivo ignorado")
			}
			return nil
		})
		if err != nil {
			logError("Erro ao processar %s: %v", podDir, err)
		}
	}
}

func extractNamespaceAndPod(path string) (string, string) {
	parts := strings.Split(filepath.Base(filepath.Dir(filepath.Dir(path))), "_")
	if len(parts) >= 2 {
		return parts[0], parts[1]
	}
	return "unknown", "unknown"
}

func fileOlderThan(path string, ageSec int) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) > time.Duration(ageSec)*time.Second
}

func isReadableAndNotEmpty(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if info.Size() == 0 {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	file.Close()
	return true
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}

func copyFile(src, dst string) {
	from, err := os.Open(src)
	if err != nil {
		logError("Erro ao abrir: %s: %v", src, err)
		return
	}
	defer from.Close()

	to, err := os.Create(dst)
	if err != nil {
		logError("Erro ao criar destino: %s: %v", dst, err)
		return
	}
	defer to.Close()

	_, err = io.Copy(to, from)
	if err != nil {
		logError("Erro ao copiar conteúdo: %v", err)
	}

	info, err := os.Stat(src)
	if err == nil {
		os.Chmod(dst, info.Mode())
		os.Chtimes(dst, time.Now(), info.ModTime())
	}
}

func shouldArchive(ns string) bool {
	if len(patternList) == 1 && patternList[0] == "*" {
		return true
	}
	for _, pattern := range patternList {
		if strings.HasSuffix(pattern, "*") {
			prefix := strings.TrimSuffix(pattern, "*")
			if strings.HasPrefix(ns, prefix) {
				return true
			}
		} else if pattern == ns {
			return true
		}
	}
	return false
}

func printHeader() {
	fmt.Printf("🟢 k8slogbck - Backup de Logs de Pods v%s\n", version)
	fmt.Println("📦 Diretório de logs:", logBase)
	fmt.Println("💾 Diretório de backup:", backupPath)
	fmt.Println()
}

func logInfo(format string, args ...any) {
	fmt.Printf(time.Now().Format("2006-01-02 15:04:05")+" ℹ️  "+format+"\n", args...)
}

func logWarn(format string, args ...any) {
	fmt.Printf(time.Now().Format("2006-01-02 15:04:05")+" ⚠️  "+format+"\n", args...)
}

func logError(format string, args ...any) {
	fmt.Printf("%s ❌ "+format+"\n", append([]any{time.Now().Format("2006-01-02 15:04:05")}, args...)...)
}

func logFatal(format string, args ...any) {
	logError(format, args...)
	os.Exit(1)
}
