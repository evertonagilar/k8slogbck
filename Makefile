# Nome e tag da imagem
IMAGE_NAME=evertonagilar/k8slogbck
TAG=1.0.0

# Comando para build da imagem
build:
	docker build -t $(IMAGE_NAME):$(TAG) .

# Comando para enviar a imagem para o registry
push:
	docker push $(IMAGE_NAME):$(TAG)

# Build + Push
publish: build push

# Limpa a imagem local
clean:
	docker rmi $(IMAGE_NAME):$(TAG) || true
