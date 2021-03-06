FROM alpine:latest

ENV HELM_VERSION="v3.1.0" \
    HELM_URL="https://get.helm.sh/helm-v3.1.0-linux-amd64.tar.gz" \
    HELM_SHA256="f0fd9fe2b0e09dc9ed190239fce892a468cbb0a2a8fffb9fe846f893c8fd09de"

ENV KUBECTL_VERSION="v1.14.10" \
    KUBECTL_URL="https://storage.googleapis.com/kubernetes-release/release/v1.14.10/bin/linux/amd64/kubectl" \
    KUBECTL_SHA256="7729c6612bec76badc7926a79b26e0d9b06cc312af46dbb80ea7416d1fce0b36"

ENV KUSTOMIZE_VERSION="v3.5.4" \
    KUSTOMIZE_URL="https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize%2Fv3.5.3/kustomize_v3.5.3_linux_amd64.tar.gz" \
    KUSTOMIZE_SHA256="5cdeb2af81090ad428e3a94b39779b3e477e2bc946be1fe28714d1ca28502f6a"

ENV KONJURE_VERSION="v0.2.0" \
    KONJURE_URL="https://github.com/carbonrelay/konjure/releases/download/v0.2.0/konjure-linux-amd64.tar.gz" \
    KONJURE_SHA256="a9812e1a29d7dca2afc3606d860c5e2f5674da6c678ca8707f9378de547f69fa"

RUN apk --no-cache add curl && \
    curl -L "$HELM_URL" | tar xz -C /usr/local/bin --exclude '*/*[^helm]' --strip-components=1 && \
    curl -L "$KUBECTL_URL" -o /usr/local/bin/kubectl && chmod +x /usr/local/bin/kubectl && \
    curl -L "$KUSTOMIZE_URL" | tar xz -C /usr/local/bin && \
    curl -L "$KONJURE_URL" | tar xz -C /usr/local/bin && \
    addgroup -g 1000 -S setup && \
    adduser -u 1000 -S setup -G setup

COPY . /workspace/

ARG IMG
ARG PULL_POLICY
ARG VERSION
RUN cd /workspace/manager && kustomize edit set image controller=${IMG} && \
    sed -i "s|VERSION|${VERSION:-unknown}|g" /workspace/install/metadata_labels.yaml && \
    sed -i "s|PULL_POLICY|${PULL_POLICY}|g" /workspace/install/manager_patch.yaml /workspace/chart/redskyops/values.yaml && \
    sed -i "s|IMG|${IMG%:*}|g" /workspace/chart/redskyops/values.yaml /workspace/chart/redskyops/questions.yml && \
    sed -i "s|TAG|${IMG##*:}|g" /workspace/chart/redskyops/values.yaml /workspace/chart/redskyops/questions.yml && \
    echo "appVersion: ${VERSION}" >> /workspace/chart/redskyops/Chart.yaml && \
    mkdir -p /workspace/base && \
    chown -R setup /workspace

USER setup:setup
RUN konjure kustomize init

# Add Helm stable until May 13, 2020
RUN helm repo add stable https://kubernetes-charts.storage.googleapis.com

WORKDIR "/workspace/base"
ENTRYPOINT ["/workspace/docker-entrypoint.sh"]
CMD ["install"]
