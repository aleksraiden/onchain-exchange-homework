package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
	mrand "math/rand"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	"github.com/google/uuid"

	// Используем v0.38.x версию CometBFT
	coregrpc "github.com/cometbft/cometbft/rpc/grpc"
	
	"tx-generator/tx" 
)

const (
	ServerAddress        = "127.0.0.1:26659"
	DefaultFlushInterval = 25 * time.Millisecond 
	TxGenerationInterval = 450 * time.Millisecond 
	PingInterval         = 1 * time.Second        // Интервал пинга
	BlobInterval		 = 150 * time.Millisecond //Интервал для MetaBlob 
	BufferSize           = 10000
	
	SendTimeout          = 10 * time.Second  // Было 2 сек, ставим 10
    FlushTimeout         = 20 * time.Second  // Было 5 сек, ставим 20
)

// -----------------------------------------------------------------------------
// GrpcClient - базовый клиент
// -----------------------------------------------------------------------------
type GrpcClient struct {
	conn   *grpc.ClientConn
	client coregrpc.BroadcastAPIClient
}

func NewGrpcClient(addr string) (*GrpcClient, error) {
	// Это предотвращает разрывы соединения (RST_STREAM) при простоях или лагах сети
    kacp := keepalive.ClientParameters{
        Time:                10 * time.Second, // Пинговать сервер каждые 10 сек, если нет активности
        Timeout:             time.Second,      // Ждать ответа на пинг 1 сек
        PermitWithoutStream: true,             // Слать пинги даже если нет активных RPC вызовов
    }
	
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(kacp), // Применяем настройки
        // Можно увеличить максимальный размер сообщения, если блобы будут > 4MB
        grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(20 * 1024 * 1024)), 
	}
	
	conn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to gRPC server: %w", err)
	}

	client := coregrpc.NewBroadcastAPIClient(conn)
	return &GrpcClient{
		conn:   conn,
		client: client,
	}, nil
}

func (c *GrpcClient) Close() {
	if c.conn != nil {
		c.conn.Close()
	}
}

func (c *GrpcClient) SendTx(ctx context.Context, txBytes []byte) (*coregrpc.ResponseBroadcastTx, error) {
	req := &coregrpc.RequestBroadcastTx{
		Tx: txBytes,
	}
	return c.client.BroadcastTx(ctx, req)
}

// НОВЫЙ МЕТОД: Ping
func (c *GrpcClient) Ping(ctx context.Context) error {
	req := &coregrpc.RequestPing{}
	_, err := c.client.Ping(ctx, req)
	return err
}

// -----------------------------------------------------------------------------
// AsyncGrpcClient - клиент с буферизацией и воркером
// -----------------------------------------------------------------------------

type AsyncGrpcClient struct {
	baseClient    *GrpcClient
	txChan        chan []byte
	flushInterval time.Duration
	stopChan      chan struct{}
}

func NewAsyncGrpcClient(addr string, flushInterval time.Duration) (*AsyncGrpcClient, error) {
	base, err := NewGrpcClient(addr)
	if err != nil {
		return nil, err
	}

	ac := &AsyncGrpcClient{
		baseClient:    base,
		txChan:        make(chan []byte, BufferSize),
		flushInterval: flushInterval,
		stopChan:      make(chan struct{}),
	}

	go ac.worker()

	return ac, nil
}

// Метод-обертка для пинга
func (ac *AsyncGrpcClient) Ping(ctx context.Context) error {
	return ac.baseClient.Ping(ctx)
}

func (ac *AsyncGrpcClient) Push(txBytes []byte) {
	select {
	case ac.txChan <- txBytes:
	default:
		log.Println("WARN: AsyncGrpcClient buffer full, dropping tx")
	}
}

func (ac *AsyncGrpcClient) Close() {
	close(ac.stopChan)
	ac.baseClient.Close()
}

func (ac *AsyncGrpcClient) worker() {
	if ac.flushInterval == 0 {
		for {
			select {
			case txBytes := <-ac.txChan:
				ac.sendOne(txBytes)
			case <-ac.stopChan:
				return
			}
		}
	}

	ticker := time.NewTicker(ac.flushInterval)
	defer ticker.Stop()

	var buffer [][]byte

	for {
		select {
		case txBytes := <-ac.txChan:
			buffer = append(buffer, txBytes)
		
		case <-ticker.C:
			if len(buffer) > 0 {
				ac.flushBuffer(buffer)
				buffer = nil 
				buffer = make([][]byte, 0, 100) 
			}

		case <-ac.stopChan:
			if len(buffer) > 0 {
				ac.flushBuffer(buffer)
			}
			return
		}
	}
}

func (ac *AsyncGrpcClient) flushBuffer(txs [][]byte) {
	ctx, cancel := context.WithTimeout(context.Background(), FlushTimeout)
	defer cancel()

	for _, txBytes := range txs {
		_, err := ac.baseClient.SendTx(ctx, txBytes)
		if err != nil {
			log.Printf("GRPC Send Error: %v", err)
		}
	}
}

func (ac *AsyncGrpcClient) sendOne(txBytes []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), SendTimeout)
	defer cancel()
	_, err := ac.baseClient.SendTx(ctx, txBytes)
	if err != nil {
		log.Printf("GRPC Send Error: %v", err)
	}
}

// ==========================================
// Тестовый запуск (Main)
// ==========================================

func main() {
	mrand.Seed( int64(time.Now().Unix()) )

	log.Println("Starting gRPC Periodic Client...")

	// 1. Создаем клиент
	client, err := NewAsyncGrpcClient(ServerAddress, DefaultFlushInterval)
	if err != nil {
		log.Fatalf("Connection error: %v", err)
	}
	defer client.Close()

	log.Printf("Connected. Flush: %v, Gen Rate: %v, Ping Rate: %v, Blob Rate: %v,", 
		DefaultFlushInterval, TxGenerationInterval, PingInterval, BlobInterval)

	// 2. Подготовка ключей
	_, privKey, _ := ed25519.GenerateKey(rand.Reader)
	signerUID := uint64(777)
	var txNonce uint64 = 0

	// 3. Тикеры
	txTicker := time.NewTicker(TxGenerationInterval)
	defer txTicker.Stop()

	pingTicker := time.NewTicker(PingInterval)
	defer pingTicker.Stop()
	
	blobTicker := time.NewTicker(BlobInterval)
	defer blobTicker.Stop()

	// 4. Сигналы
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	log.Println("Press Ctrl+C to stop...")

	// 5. Главный цикл
	for {
		select {
		case <-sigChan:
			log.Println("\nStopping client...")
			client.Close()
			log.Println("Exited.")
			return

		// --- ПИНГ (Раз в секунду) ---
		case <-pingTicker.C:
			// Запускаем в горутине, чтобы если пинг затупит, он не задержал отправку транзакции
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err := client.Ping(ctx); err != nil {
					log.Printf("⚠️ Ping Failed: %v", err)
				} else {
					// Можно убрать лог, если слишком шумно
					log.Printf("✅ Ping OK") 
				}
			}()

		// --- ГЕНЕРАЦИЯ ТРАНЗАКЦИИ (Раз в 450мс) ---
		case <-txTicker.C:
			currentNonce := atomic.AddUint64(&txNonce, 1)
			now := uint64(time.Now().Unix())

			header := &tx.TransactionHeader{
				ChainType:    tx.ChainType_LOCALNET,
				ChainVersion: 1,
				OpCode:       tx.OpCode_META_NOOP, 
				AuthType:     tx.TxAuthType_UID,
				SignerUid:    signerUID,
				Nonce:        currentNonce,
				MarketCode:   tx.Markets_UNDEFINED,
				MarketSymbol: 0,
				MinHeight:    now - 60,
				MaxHeight:    now + 60,
				Signature:    make([]byte, 64),
			}

			noopPayload := &tx.MetaNoopPayload{Payload: []byte{}}

			txx := &tx.Transaction{
				HeaderData: &tx.Transaction_Header{Header: header},
				Payload:    &tx.Transaction_MetaNoop{MetaNoop: noopPayload},
			}

			tempBytes, _ := proto.Marshal(txx)
			sig := ed25519.Sign(privKey, tempBytes)
			header.Signature = sig

			finalTxBytes, err := proto.Marshal(txx)
			if err != nil {
				log.Printf("Marshal error: %v", err)
				continue
			}

			client.Push(finalTxBytes)
			log.Printf("Refreshed NOOP sent (Nonce: %d)", currentNonce)
					
		case <-blobTicker.C:
			// Запускаем в горутине
			go func() {
				//ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				//defer cancel()
				
				currentNonce := atomic.AddUint64(&txNonce, 1)
								
				finalTxBytes, err := generateMetaBlobTx(101, currentNonce, privKey)
				if err != nil {
					log.Printf("Marshal error: %v", err)
					return
				}
				
				client.Push(finalTxBytes)
				log.Printf("META_BLOB sent (TxSize: %d Kb, Nonce: %d)", uint32(len(finalTxBytes) / 1024), currentNonce)
			}()
		}
	}
}

// Helper
func genUUIDv7() *tx.OrderID {
	id, err := uuid.NewV7()
	if err != nil {
		panic(fmt.Sprintf("failed to generate uuidv7: %v", err))
	}
	idBytes := id[:]
	return &tx.OrderID{Id: idBytes}
}

// generateMetaBlobTx создает транзакцию с случайным блоком данных (1..1MB)
func generateMetaBlobTx(signerUID uint64, nonce uint64, privKey ed25519.PrivateKey) ([]byte, error) {
	// 1. Выбираем случайный размер от 1 байта до 1 МБ (1024*1024)
	const maxBlobSize = 1024 * 1024
	size := mrand.Intn(maxBlobSize) + 1

	// 2. Генерируем случайные данные
	blob := make([]byte, size)
	// Используем crypto/rand для заполнения (или math/rand для скорости, если критично)
	_, err := mrand.Read(blob) 
	if err != nil {
		return nil, fmt.Errorf("failed to generate random blob: %w", err)
	}

	// 3. Создаем заголовок
	now := uint64(time.Now().Unix())
	header := &tx.TransactionHeader{
		ChainType:    tx.ChainType_LOCALNET,
		ChainVersion: 1,
		OpCode:       tx.OpCode_META_BLOB, // <-- Используем META_BLOB
		AuthType:     tx.TxAuthType_UID,
		SignerUid:    signerUID,
		Nonce:        nonce,
		MarketCode:   tx.Markets_UNDEFINED,
		MarketSymbol: 0,
		MinHeight:    now - 60,
		MaxHeight:    now + 60,
		Signature:    make([]byte, 64),
	}

	// 4. Создаем Payload
	blobPayload := &tx.MetaBlobPayload{
		Payload: blob,
	}

	// 5. Упаковываем в Transaction
	txx := &tx.Transaction{
		HeaderData: &tx.Transaction_Header{
			Header: header,
		},
		Payload: &tx.Transaction_MetaBlob{
			MetaBlob: blobPayload,
		},
	}

	// 6. Подписываем
	tempBytes, err := proto.Marshal(txx)
	if err != nil {
		return nil, fmt.Errorf("marshal temp failed: %w", err)
	}
	
	sig := ed25519.Sign(privKey, tempBytes)
	header.Signature = sig

	// 7. Финальная сериализация
	finalTxBytes, err := proto.Marshal(txx)
	if err != nil {
		return nil, fmt.Errorf("marshal final failed: %w", err)
	}

	return finalTxBytes, nil
}



















