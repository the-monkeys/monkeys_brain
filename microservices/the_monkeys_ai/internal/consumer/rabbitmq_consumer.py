import json
import logging
import pika
import time
import os
from typing import Optional, Dict, Any

logger = logging.getLogger(__name__)


class AIAnalysisConsumer:
    """RabbitMQ consumer for reading blogs and detecting AI content"""

    def __init__(
        self,
        rabbitmq_host: str,
        rabbitmq_port: int,
        queue_name: str,
        exchange_name: str,
        routing_key: str,
        es_client,
    ):
        self.rabbitmq_host = rabbitmq_host
        self.rabbitmq_port = rabbitmq_port
        self.queue_name = queue_name
        self.exchange_name = exchange_name
        self.routing_key = routing_key
        self.es_client = es_client

        self.connection: Optional[pika.BlockingConnection] = None
        self.channel: Optional[pika.adapters.blocking_connection.BlockingChannel] = None
        self.reconnect_delay = 1  # Start with 1 second
        self.max_reconnect_delay = 30

    def connect(self) -> bool:
        """Establish RabbitMQ connection with exponential backoff"""
        try:
            credentials = pika.PlainCredentials(
                username=os.getenv("RABBITMQ_USERNAME", "guest"),
                password=os.getenv("RABBITMQ_PASSWORD", "guest"),
            )

            parameters = pika.ConnectionParameters(
                host=self.rabbitmq_host,
                port=self.rabbitmq_port,
                credentials=credentials,
                connection_attempts=3,
                retry_delay=2,
            )

            self.connection = pika.BlockingConnection(parameters)
            self.channel = self.connection.channel()

            # Declare exchange and queue with durability
            self.channel.exchange_declare(
                exchange=self.exchange_name, exchange_type="direct", durable=True
            )

            self.channel.queue_declare(queue=self.queue_name, durable=True)

            self.channel.queue_bind(
                queue=self.queue_name,
                exchange=self.exchange_name,
                routing_key=self.routing_key,
            )

            # Set QoS - process one message at a time
            self.channel.basic_qos(prefetch_count=1)

            logger.info(
                f"✅ Connected to RabbitMQ: {self.rabbitmq_host}:{self.rabbitmq_port}"
            )
            self.reconnect_delay = 1  # Reset delay on successful connection
            return True

        except Exception as e:
            logger.error(f"❌ Failed to connect to RabbitMQ: {e}")
            return False

    def start_consuming(self):
        """Start consuming messages from the queue"""
        while True:
            try:
                # Always force a fresh connection + channel on each iteration
                self._close()
                if not self.connect():
                    logger.warning(
                        f"Retrying connection in {self.reconnect_delay}s..."
                    )
                    time.sleep(self.reconnect_delay)
                    self.reconnect_delay = min(
                        self.reconnect_delay * 2, self.max_reconnect_delay
                    )
                    continue

                self.reconnect_delay = 1  # Reset backoff on successful connect
                logger.info(f"🎧 Listening on queue: {self.queue_name}")
                self.channel.basic_consume(
                    queue=self.queue_name,
                    on_message_callback=self.on_message,
                    auto_ack=False,  # Manual acknowledgment
                )

                self.channel.start_consuming()

            except (pika.exceptions.ConnectionClosed,
                    pika.exceptions.ChannelClosed,
                    pika.exceptions.AMQPConnectionError) as e:
                logger.warning(f"RabbitMQ connection lost ({e}), reconnecting...")
                self.reconnect_delay = 1
            except Exception as e:
                logger.error(f"Unexpected error in consumer loop: {e}")
                time.sleep(self.reconnect_delay)

    def _close(self):
        """Safely close existing connection and channel"""
        try:
            if self.channel and self.channel.is_open:
                self.channel.close()
        except Exception:
            pass
        try:
            if self.connection and not self.connection.is_closed:
                self.connection.close()
        except Exception:
            pass
        self.channel = None
        self.connection = None

    def on_message(self, channel, method, properties, body):
        """Callback for processing messages from Go blog service (InterServiceMessage)"""
        try:
            # Parse InterServiceMessage from Go blog service
            message: Dict[str, Any] = json.loads(body.decode("utf-8"))

            blog_id = message.get("blog_id")
            account_id = message.get("account_id")
            action = message.get("action")
            correlation_id = message.get("correlation_id", "")
            blog_status = message.get("blog_status", "")
            priority = message.get("priority", "")
            tags = message.get("tags", [])
            ip_address = message.get("ip_address", "")
            client = message.get("client", "")

            print("\n" + "="*80)
            print("📨 RABBITMQ MESSAGE RECEIVED (InterServiceMessage)")
            print("="*80)
            print(f"  blog_id:        {blog_id}")
            print(f"  account_id:     {account_id}")
            print(f"  action:         {action}")
            print(f"  blog_status:    {blog_status}")
            print(f"  correlation_id: {correlation_id}")
            print(f"  priority:       {priority}")
            print(f"  tags:           {tags}")
            print(f"  ip_address:     {ip_address}")
            print(f"  client:         {client}")
            print("="*80)

            logger.info(
                f"📨 Received message: blog_id={blog_id}, action={action}, "
                f"correlation_id={correlation_id}"
            )

            # Validate message
            if not blog_id:
                logger.error("❌ Invalid message: missing blog_id")
                channel.basic_nack(delivery_tag=method.delivery_tag, requeue=False)
                return

            # Fetch blog from Elasticsearch
            blog_data = self.es_client.get_blog(blog_id)
            if not blog_data:
                logger.error(f"❌ Blog not found in Elasticsearch: blog_id={blog_id}")
                channel.basic_nack(delivery_tag=method.delivery_tag, requeue=False)
                return

            logger.info(f"✅ Fetched blog from Elasticsearch: {blog_id}")

            # Analyze blog JSON for AI content
            self._analyze_and_print_result(blog_id, account_id or "", blog_data, correlation_id)

            # Acknowledge message
            channel.basic_ack(delivery_tag=method.delivery_tag)

        except json.JSONDecodeError as e:
            logger.error(f"❌ Failed to parse message JSON: {e}")
            channel.basic_nack(delivery_tag=method.delivery_tag, requeue=False)
        except Exception as e:
            logger.error(f"❌ Error processing message: {e}")
            # Nack without requeue to avoid infinite loop
            channel.basic_nack(delivery_tag=method.delivery_tag, requeue=False)

    def _analyze_and_print_result(self, blog_id: str, account_id: str, blog_data: Dict[str, Any], correlation_id: str):
        """Analyze blog content, print result to terminal, and persist to ES."""
        from ..services.ai_detection import analyze_blog_json

        try:
            logger.info(f"🔍 Analyzing blog content: {blog_id}")

            result = analyze_blog_json(blog_data)

            score = result.get("score", 0.0)
            verdict = result.get("verdict", "human")
            flagged = result.get("is_flagged", False)
            features = result.get("features", {})
            evidence = result.get("feature_evidence", {})
            stats = result.get("stats", {})
            tells = result.get("tells", [])
            tell_count = result.get("tell_count", 0)

            # Visual score bar (0-10).
            filled = int(round(score))
            bar = "█" * filled + "░" * (10 - filled)

            # ---- Terminal output (LOCAL TESTING ONLY) ----
            print("\n" + "=" * 80)
            print("🤖 AI CONTENT DETECTION RESULT")
            print("=" * 80)
            print(f"Blog ID:        {blog_id}")
            print(f"Account ID:     {account_id}")
            print(f"Correlation ID: {correlation_id}")
            print("-" * 80)
            print(f"AI SCORE:       {score:.1f} / 10   [{bar}]")
            print(f"Verdict:        {verdict.upper().replace('_', ' ')}")
            print(f"Flagged:        {'⚠️  YES' if flagged else 'no'}")
            print(f"Reason:         {result.get('reason', '')}")
            print("-" * 80)

            if tells:
                print(f"AI tells detected ({tell_count}, evidence only):")
                for s in tells:
                    print(f"  ⚑ {s}")
                print("-" * 80)

            if features:
                print("Feature breakdown (0=human, 1=AI):")
                for name, val in features.items():
                    ev = evidence.get(name, "")
                    print(f"  • {name:<22} {val:>5.2f}   {ev}")
                print("-" * 80)

            print(
                f"Words: {stats.get('word_count', 0)}   "
                f"Sentences: {stats.get('sentence_count', 0)}   "
                f"Paragraphs: {stats.get('paragraph_count', 0)}   "
                f"Chars: {stats.get('text_length', 0)}"
            )
            print("=" * 80 + "\n")

            logger.info(
                f"🤖 AI score for blog {blog_id}: {score}/10 "
                f"({verdict}, flagged={flagged})"
            )

            # ---- Persist to Elasticsearch (PRODUCTION OUTPUT) ----
            self.es_client.update_ai_fields(blog_id, result, correlation_id)

        except Exception as e:
            logger.error(f"❌ Error analyzing blog: {e}")
            print(f"\n❌ Error analyzing blog {blog_id}: {e}\n")

    def close(self):
        """Close RabbitMQ connection"""
        if self.connection and not self.connection.is_closed:
            self.connection.close()
            logger.info("Closed RabbitMQ connection")
