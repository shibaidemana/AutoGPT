import asyncio
import logging
import sys
from contextlib import asynccontextmanager
from starlette.types import Scope

import uvicorn
from autogpt_libs.auth import parse_jwt_token
from fastapi import Depends, FastAPI, WebSocket, WebSocketDisconnect
from fastapi.middleware.cors import CORSMiddleware

from backend.data.queue import AsyncRedisEventQueue
from backend.data.user import DEFAULT_USER_ID
from backend.server.conn_manager import ConnectionManager
from backend.server.model import ExecutionSubscription, Methods, WsMessage
from backend.util.service import AppProcess
from backend.util.settings import Config, Settings

# Create a custom logger
logger = logging.getLogger(__name__)
logger.setLevel(logging.DEBUG)

# Create handlers
c_handler = logging.StreamHandler(sys.stdout)
f_handler = logging.FileHandler('ws_api.log')
c_handler.setLevel(logging.DEBUG)
f_handler.setLevel(logging.DEBUG)

# Create formatters and add it to handlers
log_format = '%(asctime)s - %(name)s - %(levelname)s - %(message)s'
c_formatter = logging.Formatter(log_format)
f_formatter = logging.Formatter(log_format)
c_handler.setFormatter(c_formatter)
f_handler.setFormatter(f_formatter)

# Add handlers to the logger
logger.addHandler(c_handler)
logger.addHandler(f_handler)

settings = Settings()


@asynccontextmanager
async def lifespan(app: FastAPI):
    await event_queue.connect()
    manager = get_connection_manager()
    asyncio.create_task(event_broadcaster(manager))
    yield
    await event_queue.close()


app = FastAPI(lifespan=lifespan)
event_queue = AsyncRedisEventQueue()
_connection_manager = None

logger.info(f"CORS allow origins: {settings.config.backend_cors_allow_origins}")
app.add_middleware(
    CORSMiddleware,
    allow_origins=settings.config.backend_cors_allow_origins,
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
    allow_origin_regex=r"https?://localhost:\d+",  # Add this line to allow all localhost origins
)


def get_connection_manager():
    global _connection_manager
    if _connection_manager is None:
        _connection_manager = ConnectionManager()
    return _connection_manager


async def event_broadcaster(manager: ConnectionManager):
    while True:
        event = await event_queue.get()
        if event is not None:
            await manager.send_execution_result(event)


async def authenticate_websocket(websocket: WebSocket) -> str:
    logger.info(f"Authenticating WebSocket connection: {websocket.scope}")
    if settings.config.enable_auth.lower() == "true":
        token = websocket.query_params.get("token")
        if not token:
            logger.warning("Missing authentication token")
            await websocket.close(code=4001, reason="Missing authentication token")
            return ""

        try:
            payload = parse_jwt_token(token)
            user_id = payload.get("sub")
            if not user_id:
                logger.warning("Invalid token: missing 'sub' claim")
                await websocket.close(code=4002, reason="Invalid token")
                return ""
            return user_id
        except ValueError as e:
            logger.warning(f"Invalid token: {str(e)}")
            await websocket.close(code=4003, reason="Invalid token")
            return ""
    else:
        logger.info("Authentication disabled, using default user ID")
        return DEFAULT_USER_ID


async def handle_subscribe(
    websocket: WebSocket, manager: ConnectionManager, message: WsMessage
):
    if not message.data:
        await websocket.send_text(
            WsMessage(
                method=Methods.ERROR,
                success=False,
                error="Subscription data missing",
            ).model_dump_json()
        )
    else:
        ex_sub = ExecutionSubscription.model_validate(message.data)
        await manager.subscribe(ex_sub.graph_id, websocket)
        logger.info(f"New execution subscription for graph {ex_sub.graph_id}")
        await websocket.send_text(
            WsMessage(
                method=Methods.SUBSCRIBE,
                success=True,
                channel=ex_sub.graph_id,
            ).model_dump_json()
        )


async def handle_unsubscribe(
    websocket: WebSocket, manager: ConnectionManager, message: WsMessage
):
    if not message.data:
        await websocket.send_text(
            WsMessage(
                method=Methods.ERROR,
                success=False,
                error="Subscription data missing",
            ).model_dump_json()
        )
    else:
        ex_sub = ExecutionSubscription.model_validate(message.data)
        await manager.unsubscribe(ex_sub.graph_id, websocket)
        logger.info(f"Removed execution subscription for graph {ex_sub.graph_id}")
        await websocket.send_text(
            WsMessage(
                method=Methods.UNSUBSCRIBE,
                success=True,
                channel=ex_sub.graph_id,
            ).model_dump_json()
        )


@app.get("/")
async def health():
    return {"status": "healthy"}


@app.websocket("/ws")
async def websocket_router(
    websocket: WebSocket, manager: ConnectionManager = Depends(get_connection_manager)
):
    logger.info(f"[WebSocket] New connection attempt from {websocket.client}")
    try:
        # Log the full scope for debugging
        logger.debug(f"[WebSocket] Connection scope: {websocket.scope}")
        
        user_id = await authenticate_websocket(websocket)
        if not user_id:
            logger.warning(f"[WebSocket] Authentication failed for {websocket.client}")
            return
        await manager.connect(websocket)
        logger.info(f"[WebSocket] Connected for user {user_id}")
        try:
            while True:
                data = await websocket.receive_text()
                logger.debug(f"[WebSocket] Received message: {data}")
                message = WsMessage.model_validate_json(data)
                if message.method == Methods.SUBSCRIBE:
                    await handle_subscribe(websocket, manager, message)
                elif message.method == Methods.UNSUBSCRIBE:
                    await handle_unsubscribe(websocket, manager, message)
                elif message.method == Methods.ERROR:
                    logger.error(f"[WebSocket] Error message received: {message.data}")
                else:
                    logger.warning(
                        f"[WebSocket] Unknown message type {message.method} received: "
                        f"{message.data}"
                    )
                    await websocket.send_text(
                        WsMessage(
                            method=Methods.ERROR,
                            success=False,
                            error="Message type is not processed by the server",
                        ).model_dump_json()
                    )
        except WebSocketDisconnect:
            manager.disconnect(websocket)
            logger.info(f"[WebSocket] Client disconnected: {websocket.client}")
        except Exception as e:
            logger.exception(f"[WebSocket] Unexpected error in connection: {e}")
            await websocket.close(code=1011)  # 1011 indicates a server error
    except Exception as e:
        logger.exception(f"[WebSocket] Unexpected error in connection setup: {e}")
        await websocket.close(code=1011)  # 1011 indicates a server error


class WebsocketServer(AppProcess):
    def run(self):
        logger.info(f"Starting WebSocket server on {Config().websocket_server_host}:{Config().websocket_server_port}")
        uvicorn.run(
            app,
            host=Config().websocket_server_host,
            port=Config().websocket_server_port,
            log_level="debug",
            log_config={
                "version": 1,
                "disable_existing_loggers": False,
                "formatters": {
                    "default": {
                        "()": "uvicorn.logging.DefaultFormatter",
                        "fmt": "%(asctime)s - %(name)s - %(levelprefix)s %(message)s",
                        "use_colors": None,
                    },
                    "access": {
                        "()": "uvicorn.logging.AccessFormatter",
                        "fmt": '%(asctime)s - %(name)s - %(levelprefix)s %(client_addr)s - "%(request_line)s" %(status_code)s',
                    },
                },
                "handlers": {
                    "default": {
                        "formatter": "default",
                        "class": "logging.StreamHandler",
                        "stream": "ext://sys.stderr",
                    },
                    "access": {
                        "formatter": "access",
                        "class": "logging.StreamHandler",
                        "stream": "ext://sys.stdout",
                    },
                },
                "loggers": {
                    "uvicorn": {"handlers": ["default"], "level": "INFO"},
                    "uvicorn.error": {"level": "INFO"},
                    "uvicorn.access": {"handlers": ["access"], "level": "INFO", "propagate": False},
                },
            },
        )