# simple-ingress-controller
Building simple ingress controller in go

### 1.1 Initialise operator

```bash
operator-sdk init --domain=networking.k8s.io --repo=github.com/djkormo/simple-ingress-controller --skip-go-version-check
```

### 1.2 create Ingress API

```bash
operator-sdk create api --group=networking.k8s.io --version=v1 --kind=Ingress --controller --resource
```

