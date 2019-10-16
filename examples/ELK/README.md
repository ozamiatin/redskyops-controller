# ELK Example

## Introduction
The [ELK stack](https://www.elastic.co/what-is/elk-stack) is a widely used open-source open source project used to store, manage, search and visualize logs and various data.

In this example, we demonstrate how one can effectively and efficiently tune ELK resources with a custom load using [filebeat](https://www.elastic.co/products/beats/filebeat).

## Prerequisites

You must have a Kubernetes cluster. Additionally, you will need a local configured copy of `kubectl`. This example requires more resources then the [quick start](quickstart.md) tutorial, therefore you will need something larger then a typical minikube cluster. A four node cluster with 36 total vCPUs (8 on each node) and 64GB total memory (16GB on each node) is generally sufficient.

A local install of [Kustomize](https://github.com/kubernetes-sigs/kustomize/releases) (v3.1.0+) is required to manage the objects in you cluster.

Additionally, you will to initialize Red Sky Ops in your cluster. You can download a binary for your platform from the [releases page](https://github.com/redskyops/k8s-experiment/releases) and run `redskyctl init` (while connected to your cluster). For more details, see [the installation guide](install.md).

## Example Resources

The resources for this tutorial can be found in the [`/examples/tutorial/`](https://github.com/redskyops/k8s-experiment/tree/master/examples/tutorial) directory of the `k8s-experiment` source repository.

`kustomization.yaml`
: The input to Kustomize used to build the Kubernetes object manifests for this example.

`service-account.yaml`
: This experiment will use Red Sky Ops "setup tasks". Setup tasks are a simplified way to apply bulk state changes to a cluster (i.e. installing and uninstalling an application or it's components) before and after a trial run. To use setup tasks, we will create a separate service account with additional privileges necessary to make these modifications.

`experiment.yaml`
: The actual experiment object manifest; this includes the definition of the experiment itself (in terms of assignable parameters and observable metrics) as well as the instructions for carrying out the experiment (in terms of patches and metric queries). Feel free to edit the parameter ranges and change the experiment name to avoid conflicting with other experiments in the cluster.

`config/`
: This directory contains manifests for additional cluster state required to run the experiment. For example, `config/prometheus.yaml` creates a minimal Prometheus deployment used to collect metrics during a trial run. The `config/logstash-values.yaml` are Helm values used to configure a release of Logstash from a trial setup task. Additional configuration for Filebeat (load generation) and other Prometheus exporters (use for cost estimates) are also present in the configuration directory.

## Experiment Lifecycle

For every trial, several pods will come up:

1. Cost model
2. Elasticsearch-client
3. Elasticsearch-data
4. Elasticsearch-exporter
5. Elasticsearch-master
6. Elasticsearch-test
7. Filebeat
8. Kube-state-metrics
9. Logstash
10. Node-exporter
11. Prometheus

For more information on running, monitoring and maintaining experiments, please refer to our [quickstart](https://github.com/redskyops/k8s-experiment/blob/master/docs/quickstart.md) and [experiment lifecycle](https://github.com/gramLabs/k8s-experiment/blob/master/docs/lifecycle.md) documentation.
