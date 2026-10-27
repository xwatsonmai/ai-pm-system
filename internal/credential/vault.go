// Package credential keeps repository secrets outside project/model payloads.
package credential

import (
	"ai-pm-system/internal/domain"
	"ai-pm-system/internal/storage"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type Secret struct {
	Mode       string `json:"mode"`
	Username   string `json:"username,omitempty"`
	Token      string `json:"token,omitempty"`
	PrivateKey string `json:"private_key,omitempty"`
	KnownHosts string `json:"known_hosts,omitempty"`
	PublicKey  string `json:"public_key,omitempty"`
}
type Vault struct {
	store  storage.Store
	cipher cipher.AEAD
}

func Open(path string, store storage.Store) (*Vault, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	key, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		// Never regenerate a lost key over an existing vault.
		if count, e := store.RepositoryCredentialCount(context.Background()); e != nil {
			return nil, e
		} else if count > 0 {
			return nil, errors.New("仓库凭据加密密钥缺失，请恢复备份中的 repository.key")
		}
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return nil, e
		}
		_, err = f.Write(key)
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("仓库凭据加密密钥无效")
	}
	if err = os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{store: store, cipher: aead}, nil
}
func (v *Vault) Save(ctx context.Context, owner string, secret Secret) (string, error) {
	id := domain.ID("cred_")
	b, err := json.Marshal(secret)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, v.cipher.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	encrypted := v.cipher.Seal(nonce, nonce, b, []byte(id+"\x00"+owner))
	err = v.store.PutRepositoryCredential(ctx, domain.RepositoryCredential{ID: id, OwnerID: owner, Ciphertext: encrypted})
	return id, err
}
func (v *Vault) Load(ctx context.Context, id string) (Secret, string, error) {
	record, err := v.store.GetRepositoryCredential(ctx, id)
	if err != nil {
		return Secret{}, "", err
	}
	n := v.cipher.NonceSize()
	if len(record.Ciphertext) < n {
		return Secret{}, "", errors.New("仓库凭据已损坏")
	}
	b, err := v.cipher.Open(nil, record.Ciphertext[:n], record.Ciphertext[n:], []byte(id+"\x00"+record.OwnerID))
	if err != nil {
		return Secret{}, "", errors.New("无法解密仓库凭据，请检查 repository.key")
	}
	var secret Secret
	err = json.Unmarshal(b, &secret)
	return secret, record.OwnerID, err
}
