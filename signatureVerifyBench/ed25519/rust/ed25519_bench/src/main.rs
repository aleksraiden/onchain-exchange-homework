use ed25519_dalek::{Signer, SigningKey, VerifyingKey, verify_batch};
use rand::rngs::OsRng;
use rayon::prelude::*;
use std::time::Instant;

// КОНФИГУРАЦИЯ
const NUM_UNIQUE_KEYS: usize = 30_000;
const NUM_MESSAGES: usize = 50_000;
const BATCH_SIZE: usize = 128; // Пробуем 64 или 128, лимит чтобы взалило в L1 кеш
const MSGS_PER_KEY: usize = 4;

struct SignedItem {
    pub_key: VerifyingKey,
    msg: Vec<u8>,
    sig: ed25519_dalek::Signature,
}

fn main() {
    let num_cpus = num_cpus::get();
    println!("=== RUST ULTIMATE BENCHMARK ===");
    println!("Cores: {}", num_cpus);
    println!("Batch Size: {}", BATCH_SIZE);
    println!("Msgs Per Key: {}", MSGS_PER_KEY);
    println!("===============================");

    // --- ЭТАП 1: Генерация ключей ---
    let start_gen = Instant::now();
    
    // Генерируем ключи параллельно
    let keys: Vec<(SigningKey, VerifyingKey)> = (0..NUM_UNIQUE_KEYS)
        .into_par_iter()
        .map(|_| {
            let mut csprng = OsRng;
            let signing_key = SigningKey::generate(&mut csprng);
            let verifying_key = signing_key.verifying_key();
            (signing_key, verifying_key)
        })
        .collect();

    println!("1. Keys Generated in {:.2?}", start_gen.elapsed());

    // --- ЭТАП 2: Подпись ---
    let start_sign = Instant::now();

    // Генерируем подписи параллельно
    let items: Vec<SignedItem> = (0..NUM_MESSAGES)
        .into_par_iter()
        .map(|i| {
            // Логика группировки ключей (0,0,0,0, 1,1,1,1...)
            let key_idx = (i / MSGS_PER_KEY) % NUM_UNIQUE_KEYS;
            let (signing_key, verifying_key) = &keys[key_idx];

            let msg = format!("msg-{}", i).into_bytes();
            let signature = signing_key.sign(&msg);

            SignedItem {
                pub_key: *verifying_key,
                msg,
                sig: signature,
            }
        })
        .collect();

    println!("2. Signing DONE in {:.2?}", start_sign.elapsed());

    // --- ЭТАП 3: Верификация (Rayon + verify_batch) ---
    print!("3. Verifying... ");
    let start_verify = Instant::now();

    // Rayon автоматически делит массив на чанки и раздает ядрам (Work Stealing)
    let valid = items.par_chunks(BATCH_SIZE).all(|batch| {
        let mut messages: Vec<&[u8]> = Vec::with_capacity(batch.len());
        let mut signatures: Vec<ed25519_dalek::Signature> = Vec::with_capacity(batch.len());
        let mut public_keys: Vec<VerifyingKey> = Vec::with_capacity(batch.len());

        for item in batch {
            messages.push(&item.msg);
            signatures.push(item.sig);
            public_keys.push(item.pub_key);
        }

        // Вызов пакетной проверки
        verify_batch(&messages, &signatures, &public_keys).is_ok()
    });

    let duration = start_verify.elapsed();
    println!("DONE");

    if !valid {
        panic!("Validation FAILED!");
    }

    // --- РЕЗУЛЬТАТЫ ---
    println!("\n=== RESULTS ===");
    let seconds = duration.as_secs_f64();
    let tps = NUM_MESSAGES as f64 / seconds;
    let ms_per_sig = (seconds * 1000.0) / NUM_MESSAGES as f64;

    println!("Total Time:    {:.2} ms", duration.as_millis());
    println!("Throughput:    {:.0} sigs/sec", tps);
    println!("Per Signature: {:.5} ms", ms_per_sig);
}