import logging
import os
from datetime import datetime, timezone
from typing import Optional, Dict, Any, List

from ..constants import ES_BLOG_INDEX, AI_ANALYZER_VERSION

try:
    from elasticsearch import Elasticsearch
except ImportError:
    Elasticsearch = None

logger = logging.getLogger(__name__)


class ElasticsearchClient:
    """Client for fetching blog data from Elasticsearch"""

    def __init__(self, host: str, port: int, index_name: str = ES_BLOG_INDEX):
        if Elasticsearch is None:
            logger.warning("Elasticsearch not installed. Install with: pip install elasticsearch")
            self.es = None
            return

        try:
            # Get credentials from environment
            username = os.getenv('OPENSEARCH_OS_USERNAME', 'admin')
            password = os.getenv('OPENSEARCH_OS_PASSWORD', '')
            
            kwargs = {"hosts": [f"http://{host}:{port}"]}
            if username and password:
                kwargs["basic_auth"] = (username, password)
                kwargs["verify_certs"] = False
            self.es = Elasticsearch(**kwargs)
            self.index_name = index_name

            # Verify connection
            if self.es.ping():
                logger.info(f"✅ Connected to Elasticsearch: {host}:{port}")
            else:
                logger.warning(f"⚠️ Could not ping Elasticsearch at {host}:{port}")
        except Exception as e:
            logger.error(f"❌ Failed to initialize Elasticsearch client: {e}")
            self.es = None

    def get_blog(self, blog_id: str) -> Optional[Dict[str, Any]]:
        """Fetch blog document by ID from configured blog alias/index"""
        if not self.es:
            logger.error("Elasticsearch client not available")
            return None

        try:
            response = self.es.get(index=self.index_name, id=blog_id)
            blog_source = response["_source"]
            resolved_index = response.get("_index", self.index_name)
            logger.info(
                f"✅ Fetched blog {blog_id} from alias/index '{self.index_name}' (resolved index: {resolved_index})"
            )
            return blog_source
                
        except Exception as e:
            logger.error(
                f"❌ Failed to fetch blog {blog_id} from alias/index '{self.index_name}': {e}"
            )
            return None

    def update_ai_fields(
        self, blog_id: str, ai_result: Dict[str, Any], correlation_id: str = ""
    ) -> bool:
        """Write ai_* fields onto the blog doc via partial update. Logs and
        returns False on failure so the caller can still ack the message."""
        if not self.es:
            logger.error("Elasticsearch client not available; cannot persist AI fields")
            return False

        doc = {
            "ai_score": float(ai_result.get("score", 0.0)),
            "ai_verdict": ai_result.get("verdict", "human"),
            "ai_is_flagged": bool(ai_result.get("is_flagged", False)),
            "ai_analyzed_at": datetime.now(timezone.utc).isoformat(),
            "ai_analyzer_version": ai_result.get(
                "analyzer_version", AI_ANALYZER_VERSION
            ),
            "ai_correlation_id": correlation_id,
            "ai_features": ai_result.get("features", {}),
            "ai_tells": ai_result.get("tells", []),
            "ai_tell_count": ai_result.get("tell_count", 0),
            "ai_stats": ai_result.get("stats", {}),
        }

        try:
            self.es.update(index=self.index_name, id=blog_id, doc=doc)
            logger.info(
                f"✅ Persisted AI fields for blog {blog_id} "
                f"(score={doc['ai_score']}, verdict={doc['ai_verdict']}, "
                f"flagged={doc['ai_is_flagged']})"
            )
            return True
        except Exception as e:
            logger.error(f"❌ Failed to persist AI fields for blog {blog_id}: {e}")
            return False

    def search_blogs(self, query: Dict[str, Any], size: int = 10) -> list:
        """Execute a search query"""
        if not self.es:
            logger.error("Elasticsearch client not available")
            return []

        try:
            response = self.es.search(index=self.index_name, body=query, size=size)
            return [hit["_source"] for hit in response["hits"]["hits"]]
        except Exception as e:
            logger.error(f"❌ Search failed: {e}")
            return []

    def extract_blog_content(self, blog_data: Dict[str, Any]) -> str:
        """Extract plain text content from blog blocks"""
        # Actual ES structure: _source.blog.blocks[]
        content_blocks = blog_data.get("blog", {}).get("blocks", [])
        text_content = []

        for block in content_blocks:
            block_type = block.get("type", "")
            data = block.get("data", {})

            if block_type in ("paragraph", "header"):
                text = data.get("text", "")
                if text:
                    text_content.append(text)
            elif block_type == "list":
                items = data.get("items", [])
                for item in items:
                    if isinstance(item, str):
                        text_content.append(item)

        return "\n".join(text_content)

    def extract_images(self, blog_data: Dict[str, Any]) -> list:
        """Extract image URLs from blog blocks"""
        content_blocks = blog_data.get("blog", {}).get("blocks", [])
        images = []

        for block in content_blocks:
            if block.get("type") == "image":
                image_url = block.get("data", {}).get("url")
                if image_url:
                    images.append(
                        {
                            "url": image_url,
                            "caption": block.get("data", {}).get("caption", ""),
                            "block_id": block.get("id"),
                        }
                    )

        return images
