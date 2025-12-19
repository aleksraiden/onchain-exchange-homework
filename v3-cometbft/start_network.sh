#!/bin/bash

# === КОНФИГУРАЦИЯ ===
N_NODES=5                       # Количество нод
BASE_DIR="./mytestnet"          # Папка для данных
MASTER_CONFIG="./config.toml"   # Ваш кастомный конфиг
# ====================

# Проверка наличия конфига
if [ ! -f "$MASTER_CONFIG" ]; then
    echo "ОШИБКА: Файл $MASTER_CONFIG не найден!"
    echo "Создайте его перед запуском."
    exit 1
fi

echo "--- Очистка старых данных ---"
#killall cometbft 2> /dev/null
rm -rf $BASE_DIR
mkdir -p $BASE_DIR

# --- ЭТАП 1: Инициализация и Копирование конфигов ---
echo "--- Инициализация $N_NODES нод ---"

for i in $(seq 1 $N_NODES); do
    NODE_DIR="$BASE_DIR/node$i"
    
    # 1. Инициализация (создает ключи)
    ./cometbft init --home $NODE_DIR > /dev/null 2>&1
    
    # 2. Подмена конфига на ваш мастер-файл
    cp $MASTER_CONFIG $NODE_DIR/config/config.toml
done

# --- ЭТАП 2: Синхронизация Genesis ---
# Все ноды должны иметь genesis от первой ноды
echo "--- Распространение Genesis файла ---"
GENESIS_SRC="$BASE_DIR/node1/config/genesis.json"

for i in $(seq 2 $N_NODES); do
    cp $GENESIS_SRC "$BASE_DIR/node$i/config/genesis.json"
done

# --- ЭТАП 3: Настройка Сети (Peers и Ports) ---
echo "--- Настройка портов и пиров ---"

# Получаем ID и IP первой ноды (она будет точкой входа для всех)
NODE1_ID=$(./cometbft show-node-id --home $BASE_DIR/node1)
NODE1_ADDRESS="$NODE1_ID@127.0.0.1:36656"
echo "Seed Node (Node 1): $NODE1_ADDRESS"

for i in $(seq 1 $N_NODES); do
    CONFIG_FILE="$BASE_DIR/node$i/config/config.toml"
    
    # Рассчитываем смещение портов (0 для первой, 100 для второй и т.д.)
    OFFSET=$(( ($i - 1) * 100 ))
    
    # Новые порты
    P2P_PORT=$((36656 + $OFFSET))
    RPC_PORT=$((36657 + $OFFSET))
    PROXY_PORT=$((36658 + $OFFSET))
    
    # Используем sed для замены дефолтных портов на рассчитанные
    # (Предполагаем, что в мастер-конфиге стоят дефолтные 26656/57/58)
    sed -i "s/26656/$P2P_PORT/g" $CONFIG_FILE
    sed -i "s/26657/$RPC_PORT/g" $CONFIG_FILE
    sed -i "s/26658/$PROXY_PORT/g" $CONFIG_FILE
    
    # Если это НЕ первая нода, прописываем ей persistent_peers на первую
    if [ "$i" -gt 1 ]; then
        
		sed -i "s|persistent_peers = \"\"|persistent_peers = \"$NODE1_ADDRESS\"|g" "$CONFIG_FILE"
		
		
		###sed -i "s/persistent_peers = \"\"/persistent_peers = \"$NODE1_ADDRESS\"/g" $CONFIG_FILE
    fi
done

# --- ЭТАП 4: Запуск ---
echo "--- Запуск сети ---"

# Запускаем Node 1 (Лог дублируется в консоль через tee)
echo "Запуск Node 1 (Валидатор)... Логи выводятся на экран."
##./cometbft node --home $BASE_DIR/node1 --proxy_app=kvstore 2>&1 | tee "$BASE_DIR/node1.log" &
./cometbft node --home $BASE_DIR/node1 --proxy_app=kvstore > "$BASE_DIR/node1.log" 2>&1 &
PID_LIST="$!"

# Даем первой ноде фору на старт
sleep 5

# Запускаем остальные ноды (Логи только в файлы)
for i in $(seq 2 $N_NODES); do
    echo "Запуск Node $i..."
    ./cometbft node --home $BASE_DIR/node$i --proxy_app=kvstore > "$BASE_DIR/node$i.log" 2>&1 &
    PID_LIST="$PID_LIST $!"
	
	sleep 5
done

echo ""
echo "!!! СЕТЬ ЗАПУЩЕНА ($N_NODES нод) !!!"
echo "Нажмите ENTER чтобы остановить сеть и убить процессы..."
read

# Убиваем все процессы, которые мы запустили
kill $PID_LIST
echo "Сеть остановлена."