// Bundle with esbuild and @noble/curves 1.9.7; the resulting file is embedded.
import { x25519 } from '@noble/curves/ed25519';

window.easytierCredentialPublicKey = secret => {
  const privateKey = Uint8Array.from(atob(secret), c => c.charCodeAt(0));
  if (privateKey.length !== 32) throw new Error('凭据私钥长度必须为 32 字节。');
  try {
    return btoa(String.fromCharCode(...x25519.getPublicKey(privateKey)));
  } finally {
    privateKey.fill(0);
  }
};
