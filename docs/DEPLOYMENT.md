# Deployment Guide

## GitHub Secrets 설정

Repository Settings → Secrets and variables → Actions에서 다음 2개 추가:

### 1. WIREGUARD_CONFIG
WireGuard 설정 파일 전체 내용:
```bash
cat wg0.conf  # 내용을 복사해서 Secret에 추가
```

### 2. SSH_PRIVATE_KEY
배포용 SSH 개인키:
```bash
cat ~/.ssh/deploy_key  # 내용을 복사해서 Secret에 추가
```

## 서버 설정

### 1. Deploy 사용자 생성
```bash
sudo useradd -m -s /bin/bash deploy
sudo usermod -aG sudo deploy
```

### 2. Passwordless sudo 설정
```bash
sudo visudo -f /etc/sudoers.d/deploy
```

추가:
```
deploy ALL=(ALL) NOPASSWD: /bin/systemctl start edg-core
deploy ALL=(ALL) NOPASSWD: /bin/systemctl stop edg-core
deploy ALL=(ALL) NOPASSWD: /bin/cp * /opt/edg/bin/
```

### 3. SSH 공개키 추가
```bash
sudo -u deploy mkdir -p /home/deploy/.ssh
sudo -u deploy sh -c 'cat >> /home/deploy/.ssh/authorized_keys'
# SSH 공개키를 붙여넣고 Ctrl+D
sudo chmod 600 /home/deploy/.ssh/authorized_keys
```

### 4. WireGuard 설정
서버에 WireGuard를 설정하여 GitHub Actions runner가 접속할 수 있도록 합니다.

## 배포 방법

### 자동 배포
`main` 브랜치에 push하면 자동으로 배포됩니다:
```bash
git push origin main
```

### 수동 배포
1. GitHub Actions 탭으로 이동
2. "Deploy to Dev Server" 선택
3. "Run workflow" 클릭
4. `main` 브랜치 선택 후 실행

## 트러블슈팅

### VPN 연결 실패
- GitHub Secrets의 `WIREGUARD_CONFIG` 확인
- 서버 WireGuard 상태: `sudo systemctl status wg-quick@wg0`

### SSH 연결 실패
- `SSH_PRIVATE_KEY` secret 확인
- 서버에 공개키 등록 확인: `sudo cat /home/deploy/.ssh/authorized_keys`

### 배포 실패
- GitHub Actions 로그 확인
- 서버 로그 확인: `sudo journalctl -u edg-core -n 50`
