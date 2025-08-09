#!/bin/bash

go build -o whatsapp-scheduler

if [ $? -ne 0 ]; then
    echo "Сборка не удалась"
    exit 1
else
    echo "Сборка успешна!"
fi
