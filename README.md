# simple-ingress-controller
Building simple ingress controller in go

Based on

https://www.doxsey.net/blog/how-to-build-a-custom-kubernetes-ingress-controller-in-go/

https://www.josephguadagno.net/2022/12/10/getting-started-with-developer-containers


### 1.1 Initialise operator

```bash
operator-sdk init --repo=github.com/djkormo/simple-ingress-controller --skip-go-version-check
```

### 1.2 create Ingress API

```bash
operator-sdk create api --group=networking.k8s.io --version=v1 --kind=Ingress --resource
```

### 1.3 create Secret API

```bash
operator-sdk create api --group=* --version=v1 --kind=Secret --resource
```

### 1.3 create Service API

```bash
operator-sdk create api --group=* --version=v1 --kind=Service --resource
```
